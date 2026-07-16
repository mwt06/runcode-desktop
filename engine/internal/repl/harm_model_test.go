package repl

import (
	"context"
	"testing"
)

// Judge ("smart") mode can run the harm-judge check on an independent model,
// decorrelated from the main conversation model, so the model is not asked to
// vet its own actions. When HarmModel is set, AssessHarm must use it.
func TestAssessHarmUsesIndependentModelWhenSet(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents(`{"harmful": false, "reason": "ok"}`)},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Model: "main-model", HarmModel: "judge-model"})

	if _, _, err := session.AssessHarm(context.Background(), "", "Run this shell command: go test ./..."); err != nil {
		t.Fatalf("AssessHarm: %v", err)
	}
	if got := provider.requests[0].Model; got != "judge-model" {
		t.Fatalf("harm request model = %q, want the independent judge model", got)
	}
}

// With no independent model configured, the harm check reuses the main model.
func TestAssessHarmFallsBackToMainModel(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents(`{"harmful": false, "reason": "ok"}`)},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Model: "main-model"})

	if _, _, err := session.AssessHarm(context.Background(), "", "cmd"); err != nil {
		t.Fatalf("AssessHarm: %v", err)
	}
	if got := provider.requests[0].Model; got != "main-model" {
		t.Fatalf("harm request model = %q, want the main model", got)
	}
}
