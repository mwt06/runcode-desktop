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

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
)

// Drive the actual App/host/OpenAI adapter, not a fake engine. All configuration,
// model traffic and files are isolated from the signed-in desktop and real OA.
func TestDesktopContextCompactionContinuesWithThinking(t *testing.T) {
	for _, key := range []string{"APPDATA", "XDG_CONFIG_HOME", "HOME", "USERPROFILE"} {
		t.Setenv(key, t.TempDir())
	}
	const insight = "思考中的独有判断：应剔除预付款，下一步检查第七附表"
	thinking := strings.Repeat("分析", 13000) + insight
	var mainCalls, summaryCalls atomic.Int32
	var sawThinking atomic.Bool
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			MaxTokens int               `json:"max_tokens"`
			Tools     []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		var system string
		if len(req.Messages) > 0 {
			_ = json.Unmarshal(req.Messages[0].Content, &system)
		}
		var delta map[string]any
		finish := "stop"
		switch {
		case strings.HasPrefix(system, "You are condensing"):
			summaryCalls.Add(1)
			payload, _ := json.Marshal(req.Messages)
			body := "已完成首次 Read。从 next.txt 继续，不要重复首个 Read。"
			if strings.Contains(string(payload), insight) {
				sawThinking.Store(true)
				body += insight
			}
			delta = map[string]any{"content": body}
		case len(req.Tools) == 0:
			delta = map[string]any{"content": "上下文验收"}
		default:
			n := mainCalls.Add(1)
			if req.MaxTokens > 32768 {
				t.Error("wire output allowance exceeded the whole window")
			}
			switch n {
			case 1, 2:
				path, id := "fixture.txt", "read-first"
				if n == 2 {
					path, id = "next.txt", "read-next"
					payload, _ := json.Marshal(req.Messages)
					if !strings.Contains(string(payload), insight) {
						t.Error("continued request lost reasoning insight")
					}
				}
				args, _ := json.Marshal(map[string]string{"path": path})
				delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": "Read", "arguments": string(args)}}}}
				if n == 1 {
					delta["reasoning_content"] = thinking
				}
				finish = "tool_calls"
			default:
				delta = map[string]any{"content": "原任务已完成"}
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
	workspace := t.TempDir()
	for _, name := range []string{"fixture.txt", "next.txt"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("only local fixture data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sink := &recordingSink{}
	app := New(sink)
	info, err := app.StartSession(StartSessionRequest{
		CWD: workspace, Provider: "openai", Model: "context-fixture", BaseURL: model.URL + "/v1", APIKey: "synthetic",
		PermissionMode: "safe", MaxContextTokens: 32768, MaxTokens: 81920,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.CloseSession(info.SessionID)
	if err := app.SendMessage(info.SessionID, "读取后依据思考中的结论继续核验"); err != nil {
		t.Fatal(err)
	}
	ends := waitForTurns(t, sink, 1, time.Now().Add(20*time.Second))
	if ends[0].Text != "原任务已完成" || ends[0].ContextTokens >= 32768*8/10 || mainCalls.Load() != 3 || summaryCalls.Load() < 1 || !sawThinking.Load() {
		t.Fatalf("did not compact and continue: end=%+v main=%d summary=%d", ends[0], mainCalls.Load(), summaryCalls.Load())
	}
	var started, completed, nextTool uint64
	reads := map[string]int{}
	sink.mu.Lock()
	for _, ev := range sink.events {
		env, ok := ev.data.(protocol.Envelope)
		if !ok || env.SessionID != info.SessionID {
			continue
		}
		switch payload := env.Payload.(type) {
		case protocol.ContextCompaction:
			if payload.Phase == "started" {
				started = env.Seq
			}
			if payload.Phase == "completed" {
				completed = env.Seq
				if payload.AfterTokens >= payload.MaxContextTokens*8/10 {
					t.Error("compaction completed above threshold")
				}
			}
		case protocol.ToolEvent:
			if payload.Type == "started" && payload.ToolName == "Read" {
				reads[payload.ToolUseID]++
				if payload.ToolUseID == "read-next" {
					nextTool = env.Seq
				}
			}
		}
	}
	sink.mu.Unlock()
	if started == 0 || completed <= started || nextTool <= completed || reads["read-first"] != 1 || reads["read-next"] != 1 {
		t.Fatalf("bad ordering or repeated tool: start=%d completed=%d next=%d reads=%v", started, completed, nextTool, reads)
	}
	if err := app.CloseSession(info.SessionID); err != nil {
		t.Fatal(err)
	}
	history, err := sessions.LoadHistory(workspace, info.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range history {
		for _, block := range message.Content {
			if block.Type == llm.ContentBlockTypeThinking && block.Text == thinking {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("full original thinking not retained in the persisted JSONL")
	}
}
