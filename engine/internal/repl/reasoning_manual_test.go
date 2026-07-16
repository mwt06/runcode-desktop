package repl

import (
	"context"
	"testing"

	"github.com/wt68/runcode/engine/internal/prompt"
)

// Manual reasoning (Enabled without AutoClassify) applies the chosen scenario's
// guidance directly, with no extra classification model call.
func TestRunTurnManualReasoningSkipsClassification(t *testing.T) {
	t.Parallel()

	// An in-turn scenario with a single provider response: if a classification call
	// were made, the provider would be asked twice and error on the second.
	provider := newFakeProvider(textEvents("done"), nil)
	session := newTestSession(t, SessionOptions{
		Provider:  provider,
		Model:     "mock-model",
		Prompt:    prompt.AssemblerOpts{CWD: "/tmp/runcode", Date: "2026-06-25"},
		Reasoning: ReasoningOptions{Enabled: true, DefaultScenario: ReasoningScenarioTroubleshooting},
	})

	result, err := session.RunTurn(context.Background(), "debug the failing test")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1 (no classification call in manual mode)", len(provider.requests))
	}
	if !requestSystemContains(provider.requests[0], "本回合启用") {
		t.Fatal("system prompt missing the manual in-turn protocol instruction")
	}
	if result.ReasoningClassification == nil || result.ReasoningClassification.Scenario != ReasoningScenarioTroubleshooting {
		t.Fatalf("classification = %#v, want manual troubleshooting", result.ReasoningClassification)
	}
}

// A pre-turn scenario (architecture) runs a structured analysis pass whose filled
// result grounds the main turn — no classification call in manual mode, but one
// analysis call before the main request.
func TestSessionRunTurnPreTurnStructuredAnalysis(t *testing.T) {
	t.Parallel()

	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents(`{"goal":"清晰的会话边界","constraints":"兼容现有 API","options":"A 方案/B 方案","tradeoffs":"A 简单 B 可扩展","recommendation":"选 B","risks":"迁移成本"}`)},
		fakeProviderResponse{events: textEvents("done")},
	)
	session := newTestSession(t, SessionOptions{
		Provider:  provider,
		Model:     "mock-model",
		Prompt:    prompt.AssemblerOpts{CWD: "/tmp/runcode", Date: "2026-06-25"},
		Reasoning: ReasoningOptions{Enabled: true, DefaultScenario: ReasoningScenarioArchitecture},
	})

	_, err := session.RunTurn(context.Background(), "design the module boundaries")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("provider requests = %d, want 2 (analysis pass + main, no classification)", len(provider.requests))
	}
	// The analysis pass asked the model to fill the architecture steps.
	if !requestSystemContains(provider.requests[0], "结构化分析") {
		t.Fatalf("analysis pass missing its prompt: %#v", provider.requests[0].System)
	}
	// The main turn is grounded in the rendered, filled analysis.
	mainReq := provider.requests[1]
	if !requestSystemContains(mainReq, "完成如下结构化分析") || !requestSystemContains(mainReq, "选 B") {
		t.Fatalf("main request not grounded in the filled analysis: %#v", mainReq.System)
	}
}
