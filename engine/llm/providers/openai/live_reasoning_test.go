package openai

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wt68/runcode/engine/llm"
)

// TestLiveReasoningEmitsThinking exercises the real provider (convert + HTTP/SSE +
// stream parser) against the configured OpenAI-compatible endpoint, proving that
// setting Thinking.Effort makes reasoning arrive as thinking deltas. It is skipped
// unless RUNCODE_LIVE=1 so normal test runs never hit the network.
func TestLiveReasoningEmitsThinking(t *testing.T) {
	if os.Getenv("RUNCODE_LIVE") != "1" {
		t.Skip("set RUNCODE_LIVE=1 to run the live endpoint probe")
	}
	cfgPath := filepath.Join(os.Getenv("APPDATA"), "runcode", "desktop.json")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read desktop.json: %v", err)
	}
	var cfg struct {
		Model, BaseURL, APIKey, AuthToken string
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse desktop.json: %v", err)
	}

	provider, err := New(Options{APIKey: cfg.APIKey, AuthToken: cfg.AuthToken, BaseURL: cfg.BaseURL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req := llm.Request{
		Model:     cfg.Model,
		MaxTokens: 512,
		Thinking:  llm.ThinkingConfig{Effort: llm.ThinkingHigh},
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "9.11 and 9.9 — which is larger? Think, then answer."}},
		}},
	}
	stream, err := provider.Stream(ctx, req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var textLen, thinkLen int
	for ev := range stream.Events() {
		if ev.Delta == nil {
			continue
		}
		textLen += len(ev.Delta.Text)
		thinkLen += len(ev.Delta.Thinking)
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream err: %v", err)
	}
	t.Logf("provider streamed text=%d thinking=%d chars", textLen, thinkLen)
	if thinkLen == 0 {
		t.Fatal("expected thinking deltas from the provider with Thinking.Effort=high, got none")
	}
}
