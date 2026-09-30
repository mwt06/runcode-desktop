package desktop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
)

func TestQuestionRegenerationActualRequestAndResume(t *testing.T) {
	for _, key := range []string{"APPDATA", "XDG_CONFIG_HOME", "HOME", "USERPROFILE"} {
		t.Setenv(key, t.TempDir())
	}
	var calls atomic.Int32
	var branchRequest atomic.Value
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages json.RawMessage   `json:"messages"`
			Tools    []json.RawMessage `json:"tools"`
			Model    string            `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		delta := map[string]any{"content": "synthetic title"}
		finish := "stop"
		if len(req.Tools) > 0 {
			switch calls.Add(1) {
			case 1:
				delta = map[string]any{"reasoning_content": "original reasoning retained", "tool_calls": []any{map[string]any{"index": 0, "id": "read-once", "type": "function", "function": map[string]any{"name": "Read", "arguments": `{"path":"fixture.txt"}`}}}}
				finish = "tool_calls"
			case 2:
				delta = map[string]any{"content": "prior answer retained"}
			case 3:
				delta = map[string]any{"content": "REPLACED ANSWER MUST NOT APPEAR"}
			case 4:
				delta = map[string]any{"content": "LATER ANSWER MUST NOT APPEAR"}
			default:
				branchRequest.Store(string(req.Messages))
				if req.Model != "question-fixture" {
					t.Errorf("wrong source model: %s", req.Model)
				}
				delta = map[string]any{"content": "new branch answer"}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []any{
			map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}},
			map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}},
		} {
			data, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "fixture.txt"), []byte("prior tool result retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(ws, "original.png")
	if err := os.WriteFile(image, []byte{1, 2, 3, 4}, 0o600); err != nil {
		t.Fatal(err)
	}
	sink := &recordingSink{}
	app := New(sink)
	defer func() {
		for _, id := range app.mgr.List() {
			_ = app.CloseSession(id)
		}
	}()
	info, err := app.StartSession(StartSessionRequest{CWD: ws, Provider: "openai", Model: "question-fixture", BaseURL: model.URL + "/v1", APIKey: "synthetic", PermissionMode: "safe"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := app.SubmitQuestion(SubmitQuestionRequest{SessionID: info.SessionID, Text: "read fixture"})
	if err != nil || first.QuestionID == "" {
		t.Fatalf("first: %+v %v", first, err)
	}
	waitForTurns(t, sink, 1, time.Now().Add(10*time.Second))
	second, err := app.SubmitQuestion(SubmitQuestionRequest{SessionID: info.SessionID, Text: "original second question", ImagePaths: []string{image}})
	if err != nil {
		t.Fatal(err)
	}
	waitForTurns(t, sink, 2, time.Now().Add(10*time.Second))
	if _, err := app.SubmitQuestion(SubmitQuestionRequest{SessionID: info.SessionID, Text: "later question excluded"}); err != nil {
		t.Fatal(err)
	}
	waitForTurns(t, sink, 3, time.Now().Add(10*time.Second))
	sourcePath := filepath.Join(ws, ".runcode", "sessions", info.SessionID+".jsonl")
	before, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	ref := QuestionReference{SessionID: info.SessionID, QuestionID: second.QuestionID}
	draft, err := app.GetQuestion(ref)
	if err != nil || draft.Text != "original second question" || len(draft.Images) != 1 {
		t.Fatalf("draft: %+v %v", draft, err)
	}
	if err := os.Remove(image); err != nil {
		t.Fatal(err)
	}
	// A compacted working set is not a source of truth for branching.
	source, err := app.engineSessionOf(info.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	source.ResetHistory()
	other, err := app.OpenSession("")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.SetModel(other.SessionID, "other-focused-model"); err != nil {
		t.Fatal(err)
	}
	// Process-global connection snapshots must never decide this branch's endpoint.
	app.mu.Lock()
	app.config.BaseURL, app.liveConfig.BaseURL = "http://127.0.0.1:1/wrong", "http://127.0.0.1:1/wrong"
	app.mu.Unlock()
	if _, err := app.SetPlanMode(info.SessionID, true); err != nil {
		t.Fatal(err)
	}
	if err := writeOALock(ws, info.SessionID, "question-fixture"); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.entryLocked(info.SessionID).oaLocalModel = "question-fixture"
	app.mu.Unlock()
	firstFork, err := app.ForkQuestion(QuestionReference{SessionID: info.SessionID, QuestionID: first.QuestionID})
	if err != nil || len(firstFork.Blocks) != 0 || !firstFork.Info.PlanMode {
		t.Fatalf("empty prefix lost settings: %+v %v", firstFork, err)
	}
	if err := app.CloseSession(firstFork.Info.SessionID); err != nil {
		t.Fatal(err)
	}
	fork, err := app.ForkQuestion(ref)
	if err != nil {
		t.Fatal(err)
	}
	if app.focusedSessionID() != other.SessionID || fork.Info.Model != "question-fixture" || !fork.Info.PlanMode {
		t.Fatalf("focus/config changed: %+v", fork.Info)
	}
	if lock, ok := readOALock(ws, fork.Info.SessionID); !ok || lock.LocalModel != "question-fixture" {
		t.Fatal("OA lock not inherited")
	}
	seenThinking := false
	for _, b := range fork.Blocks {
		if b.Thinking == "original reasoning retained" {
			seenThinking = true
		}
	}
	if !seenThinking {
		t.Fatal("replay dropped thinking")
	}
	after, _ := os.ReadFile(sourcePath)
	if string(before) != string(after) {
		t.Fatal("source history rewritten")
	}
	if err := app.CloseSession(info.SessionID); err != nil {
		t.Fatal(err)
	}
	for _, indexes := range [][]int{{-1}, {1}, {0, 0}} {
		if _, err := app.SubmitQuestion(SubmitQuestionRequest{SessionID: fork.Info.SessionID, Text: "invalid", Source: &ref, OriginalImages: indexes}); err == nil {
			t.Fatal("accepted invalid image reference")
		}
	}
	receipt, err := app.SubmitQuestion(SubmitQuestionRequest{SessionID: fork.Info.SessionID, Text: "edited second question", Source: &ref, OriginalImages: []int{0}})
	if err != nil || !receipt.StartedTurn {
		t.Fatalf("retry: %+v %v", receipt, err)
	}
	waitForTurns(t, sink, 4, time.Now().Add(10*time.Second))
	wire, _ := branchRequest.Load().(string)
	for _, want := range []string{"edited second question", "prior answer retained", "prior tool result retained", "AQIDBA=="} {
		if !strings.Contains(wire, want) {
			t.Errorf("missing %q in %s", want, wire)
		}
	}
	for _, bad := range []string{"original second question", "REPLACED ANSWER", "later question excluded", "LATER ANSWER", "compacted working set only"} {
		if strings.Contains(wire, bad) {
			t.Errorf("old suffix sent: %q", bad)
		}
	}
	if _, err := app.SubmitQuestion(SubmitQuestionRequest{SessionID: fork.Info.SessionID, Text: "duplicate retry", Source: &ref}); err == nil {
		t.Fatal("reused an already executed branch")
	}
	history, err := sessions.LoadHistory(ws, fork.Info.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range history {
		if m.ID == receipt.QuestionID {
			found = true
		}
	}
	if !found {
		t.Fatal("new question identity not persisted")
	}
	if err := app.CloseSession(fork.Info.SessionID); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.config.BaseURL = model.URL + "/v1"
	app.mu.Unlock()
	resumed, err := app.ResumeSession(fork.Info.SessionID)
	if err != nil || resumed.Source == nil || *resumed.Source != ref {
		t.Fatalf("resume lineage: %+v %v", resumed.Source, err)
	}
	found = false
	for _, b := range resumed.Blocks {
		if b.QuestionID == receipt.QuestionID && len(b.Images) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("resume lost question or image")
	}
}
