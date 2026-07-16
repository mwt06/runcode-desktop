package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wt68/runcode/engine/llm"
)

// TestProviderEndToEndOverHTTP exercises the real transport: an httptest server
// streams an OpenAI-style SSE response (text + an incrementally-delivered tool
// call + usage), and the provider is driven through the actual net/http + SSE
// path, then the neutral events are reassembled the way the session loop does.
func TestProviderEndToEndOverHTTP(t *testing.T) {
	t.Parallel()

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer is not a flusher")
		}
		lines := []string{
			`{"choices":[{"delta":{"role":"assistant","content":"Hi"}}]}`,
			`{"choices":[{"delta":{"content":" there"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Read","arguments":""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.go\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":5}}`,
			"[DONE]",
		}
		for _, line := range lines {
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	provider, err := New(Options{BaseURL: srv.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	stream, err := provider.Stream(context.Background(), llm.Request{
		Model:    "qwen3-test",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "read a.go"}}}},
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer stream.Close()

	text, toolID, toolName, toolArgs, stop, usage := reassemble(t, stream)

	if !strings.Contains(gotBody, `"model":"qwen3-test"`) || !strings.Contains(gotBody, `"stream":true`) {
		t.Fatalf("request body wrong: %s", gotBody)
	}
	if text != "Hi there" {
		t.Fatalf("text = %q, want %q", text, "Hi there")
	}
	if toolID != "call_1" || toolName != "Read" || toolArgs != `{"path":"a.go"}` {
		t.Fatalf("tool call = id:%q name:%q args:%q", toolID, toolName, toolArgs)
	}
	if stop != llm.StopReasonToolUse {
		t.Fatalf("stop reason = %q, want tool_use", stop)
	}
	if usage == nil || usage.InputTokens != 7 || usage.OutputTokens != 5 {
		t.Fatalf("usage = %#v", usage)
	}
}

// reassemble mirrors the session loop's block accumulation so the test asserts
// against the same materialized result the agent would see.
func reassemble(t *testing.T, stream llm.Stream) (text, toolID, toolName, toolArgs string, stop llm.StopReason, usage *llm.Usage) {
	t.Helper()
	type acc struct {
		typ  llm.ContentBlockType
		id   string
		name string
		text strings.Builder
		args strings.Builder
	}
	blocks := map[int]*acc{}
	for e := range stream.Events() {
		switch e.Type {
		case llm.StreamEventTypeContentBlockStart:
			blocks[e.Index] = &acc{typ: e.Block.Type, id: e.Block.ID, name: e.Block.Name}
		case llm.StreamEventTypeContentBlockDelta:
			b := blocks[e.Index]
			if b == nil || e.Delta == nil {
				continue
			}
			b.text.WriteString(e.Delta.Text)
			b.args.Write(e.Delta.InputJSON)
		case llm.StreamEventTypeMessageStop:
			stop = e.StopReason
			usage = e.Usage
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream err: %v", err)
	}
	for i := 0; i < len(blocks); i++ {
		b := blocks[i]
		switch b.typ {
		case llm.ContentBlockTypeText:
			text += b.text.String()
		case llm.ContentBlockTypeToolUse:
			toolID, toolName, toolArgs = b.id, b.name, b.args.String()
		}
	}
	return text, toolID, toolName, toolArgs, stop, usage
}
