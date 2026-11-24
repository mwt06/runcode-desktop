package desktop

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/imagegentool"
	appwire "github.com/wt68/runcode/internal/protocol"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
)

func completedImageReplaySession(t *testing.T) (*imageGenHostQuestionSession, imageGenerationLedger) {
	t.Helper()
	f := startImageGenHostQuestionSession(t, false)
	receipt := waitImageGenerationSubmit(t, f, "请生成一张图片")
	waitForTurns(t, f.sink, 1, time.Now().Add(10*time.Second))
	records, err := listImageGenerationLedgers(f.cwd, f.id)
	if err != nil || len(records) != 1 || records[0].Status != "succeeded" || records[0].QuestionID != receipt.QuestionID {
		t.Fatalf("completed generation: records=%+v err=%v", records, err)
	}
	// Public forks validate their chat model against the fake Bridge catalog.
	if err := f.app.mgr.SetModel(f.id, "chat-legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.SaveModelContextSettings(SaveModelContextSettingsRequest{
		Model:     platformModelRef("tenant-a", "chat-legacy"),
		Overrides: ModelContextOverride{MaxContextTokens: contextInt(32000), MaxOutputTokens: contextInt(4096), MaxHistoryMessages: contextInt(0)},
	}); err != nil {
		t.Fatal(err)
	}
	// The host fixture creates the engine directly; provide the same saved
	// connection snapshot that App.StartSession publishes for public replay.
	cfg := engine.Config{CWD: f.cwd, Provider: "openai", Model: "chat-legacy", BaseURL: f.modelBridge + "/v1", APIKey: "fixture", PermissionMode: "interactive", PersistSession: true, SessionBackend: sessions.BackendJSONL, MaxIterations: 3}
	f.app.mu.Lock()
	f.app.config, f.app.workspace = cfg, f.cwd
	f.app.entryLocked(f.id).connectionConfig = cfg
	f.app.mu.Unlock()
	return f, records[0]
}

func rewriteImageReplayHistory(t *testing.T, workspace, sessionID, mode string) {
	t.Helper()
	history, err := sessions.LoadHistory(workspace, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range history {
		content := history[i].Content[:0]
		for _, block := range history[i].Content {
			if mode == "ledger only" && (block.Type == llm.ContentBlockTypeToolUse || block.Type == llm.ContentBlockTypeToolResult) {
				continue
			}
			if mode == "damaged content" && block.Type == llm.ContentBlockTypeToolResult {
				block.Text, block.Content = "{", nil
			}
			content = append(content, block)
		}
		history[i].Content = content
	}
	path := filepath.Join(workspace, ".runcode", "sessions", sessionID+".jsonl")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	store, err := sessions.OpenJSONL(workspace, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(context.Background(), history); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func assertReplayedImage(t *testing.T, f *imageGenHostQuestionSession, resumed ResumedSession, questionID, status, accounting string) *appwire.ImageGenerationResult {
	t.Helper()
	count, hasQuestion := 0, false
	var result *appwire.ImageGenerationResult
	for _, block := range resumed.Blocks {
		if block.Kind == "user" && block.QuestionID == questionID {
			hasQuestion = true
		}
		if block.Tool == nil || block.Tool.ToolName != imagegentool.Name {
			continue
		}
		count++
		result = block.Tool.ImageGeneration
		if result == nil || result.Status != status || result.AccountingStatus != accounting || result.SubmissionStatus != "attempted" {
			t.Fatalf("replayed card=%+v, want status=%s accounting=%s submission=attempted", result, status, accounting)
		}
		if block.Tool.IsError != (status != "succeeded") {
			t.Fatalf("replayed error=%v status=%s", block.Tool.IsError, status)
		}
	}
	if !hasQuestion || count != 1 {
		t.Fatalf("question retained=%v, generated card count=%d", hasQuestion, count)
	}
	if status == "succeeded" {
		readGeneratedImage(t, f.cwd, *result)
		ref, err := f.app.OpenArtifact(resumed.Info.SessionID, result.Images[0].Path)
		if err != nil || ref.Path != result.Images[0].Path || ref.SessionID != resumed.Info.SessionID {
			t.Fatalf("replayed preview ref=%+v err=%v", ref, err)
		}
		raw, err := f.app.ReadArtifactBytes(ref.Reference)
		if err != nil {
			t.Fatal(err)
		}
		data, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
			t.Fatalf("replayed preview is not PNG: bytes=%d err=%v", len(data), err)
		}
	} else if len(result.Images) != 0 || result.Code != "unknown_outcome" {
		t.Fatalf("interrupted card=%+v", result)
	}
	return result
}

func TestImageGenerationResumeSessionPublicReplay(t *testing.T) {
	for _, mode := range []string{"normal", "ledger only", "damaged content", "missing ledger", "damaged ledger", "interrupted ledger", "ledger overrides history"} {
		t.Run(mode, func(t *testing.T) {
			f, record := completedImageReplaySession(t)
			if err := f.app.CloseSession(f.id); err != nil {
				t.Fatal(err)
			}
			// Deliberately disagree with persisted tool JSON: the ledger's
			// accounting observation must remain authoritative when readable.
			record.AccountingStatus = "verified"
			status, accounting := "succeeded", "verified"
			if mode == "interrupted ledger" || mode == "ledger overrides history" {
				record.Status, record.Result = "submitted", nil
				status = "unknown"
			}
			if err := writeImageGenerationLedger(f.cwd, record); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "ledger only", "damaged content":
				rewriteImageReplayHistory(t, f.cwd, f.id, mode)
			case "interrupted ledger":
				rewriteImageReplayHistory(t, f.cwd, f.id, "ledger only")
			case "missing ledger", "damaged ledger":
				accounting = "unverified"
				path, err := imageGenerationLedgerPath(f.id, record.GenerationID)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "missing ledger" {
					err = os.Remove(filepath.Join(f.cwd, path))
				} else {
					err = os.WriteFile(filepath.Join(f.cwd, path), []byte("{"), 0o600)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			calls := f.hostCalls.Load()
			for attempt := 0; attempt < 2; attempt++ {
				resumed, err := f.app.ResumeSession(f.id)
				if err != nil {
					t.Fatal(err)
				}
				result := assertReplayedImage(t, f, resumed, record.QuestionID, status, accounting)
				if result.GenerationID != record.GenerationID || result.RequestID != "brg-synthetic-0002" {
					t.Fatalf("replay changed durable identity: %+v", result)
				}
				if err := f.app.CloseSession(f.id); err != nil {
					t.Fatal(err)
				}
			}
			if f.bridge.posts.Load() != 1 || f.hostCalls.Load() != calls {
				t.Fatalf("replay executed generation: posts=%d hostCalls=%d", f.bridge.posts.Load(), f.hostCalls.Load())
			}
			if err := reserveImageGenerationTurn(f.cwd, f.id, record.TurnID, "gen_duplicate"); !errors.Is(err, errImageGenerationTurnSpent) {
				t.Fatalf("replay renewed consumed budget: %v", err)
			}
		})
	}
}

func TestImageGenerationForkQuestionPublicReplay(t *testing.T) {
	f, record := completedImageReplaySession(t)
	second := waitImageGenerationSubmit(t, f, "编辑这条后续问题")
	waitForTurns(t, f.sink, 2, time.Now().Add(10*time.Second))
	// A branch owns no generation ledger. Its historical result must fall back
	// to the bounded tool JSON, while retaining the source question identity.
	record.AccountingStatus = "verified"
	if err := writeImageGenerationLedger(f.cwd, record); err != nil {
		t.Fatal(err)
	}
	ref := QuestionReference{SessionID: f.id, QuestionID: second.QuestionID}
	calls := f.hostCalls.Load()
	fork, err := f.app.ForkQuestion(ref)
	if err != nil {
		t.Fatal(err)
	}
	if fork.Source == nil || *fork.Source != ref || f.app.focusedSessionID() != f.id {
		t.Fatalf("fork changed lineage/focus: source=%+v focus=%s", fork.Source, f.app.focusedSessionID())
	}
	for _, block := range fork.Blocks {
		if block.QuestionID == second.QuestionID {
			t.Fatal("fork retained replaced question")
		}
	}
	assertReplayedImage(t, f, fork, record.QuestionID, "succeeded", "unverified")
	for attempt := 0; attempt < 2; attempt++ {
		if err := f.app.CloseSession(fork.Info.SessionID); err != nil {
			t.Fatal(err)
		}
		fork, err = f.app.ResumeSession(fork.Info.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if fork.Source == nil || *fork.Source != ref {
			t.Fatalf("resumed fork lost lineage: %+v", fork.Source)
		}
		assertReplayedImage(t, f, fork, record.QuestionID, "succeeded", "unverified")
	}
	childRecords, err := listImageGenerationLedgers(f.cwd, fork.Info.SessionID)
	if err != nil || len(childRecords) != 0 || f.bridge.posts.Load() != 1 || f.hostCalls.Load() != calls {
		t.Fatalf("fork replay created an effect: ledgers=%+v posts=%d hostCalls=%d err=%v", childRecords, f.bridge.posts.Load(), f.hostCalls.Load(), err)
	}
	if err := reserveImageGenerationTurn(f.cwd, f.id, record.TurnID, "gen_duplicate"); !errors.Is(err, errImageGenerationTurnSpent) {
		t.Fatalf("fork replay renewed source budget: %v", err)
	}
}
