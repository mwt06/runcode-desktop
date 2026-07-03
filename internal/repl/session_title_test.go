package repl

import (
	"context"
	"testing"

	"github.com/wt68/runcode/internal/prompt"
)

func TestGenerateTitleSummarizesRequest(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(textEvents("打印素数脚本"), nil)
	session := newTestSession(t, SessionOptions{
		Provider: provider,
		Model:    "mock-model",
		Prompt:   prompt.AssemblerOpts{CWD: "/tmp/runcode", Date: "2026-06-23"},
	})

	title, err := session.GenerateTitle(context.Background(), "帮我写个python脚本打印100以内素数并执行")
	if err != nil {
		t.Fatalf("GenerateTitle: %v", err)
	}
	if title != "打印素数脚本" {
		t.Fatalf("title = %q, want the model's summary", title)
	}

	// Empty input returns an empty title without issuing a request (the provider
	// has no second stream queued, so a request here would error).
	title, err = session.GenerateTitle(context.Background(), "   ")
	if err != nil {
		t.Fatalf("GenerateTitle(empty): %v", err)
	}
	if title != "" {
		t.Fatalf("title = %q, want empty for blank input", title)
	}
}
