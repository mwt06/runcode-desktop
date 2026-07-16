package repl

import (
	"context"
	"testing"
)

// With HarmVotes > 1 the harm check takes the median risk across independent
// samples, so a single fooled "low" cannot pass an action the others rate high.
func TestAssessHarmMajorityVoteHarmful(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents(`{"risk": "low", "reason": "看着没事"}`)},
		fakeProviderResponse{events: textEvents(`{"risk": "high", "reason": "会删数据"}`)},
		fakeProviderResponse{events: textEvents(`{"risk": "high", "reason": "会删数据"}`)},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Model: "m", HarmVotes: 3})

	risk, _, err := session.AssessHarm(context.Background(), "", "rm -rf /data")
	if err != nil {
		t.Fatalf("AssessHarm: %v", err)
	}
	if harmRiskRank(risk) < harmRiskRank("medium") {
		t.Fatalf("median of [low, high, high] = %q, want a prompt-worthy tier", risk)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("provider requests = %d, want 3 (one per vote)", len(provider.requests))
	}
}

// The median still wins the other way: a lone high vote does not block an action
// the majority rates low.
func TestAssessHarmMajorityVoteSafe(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents(`{"risk": "low", "reason": "常规"}`)},
		fakeProviderResponse{events: textEvents(`{"risk": "low", "reason": "常规"}`)},
		fakeProviderResponse{events: textEvents(`{"risk": "high", "reason": "误判"}`)},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Model: "m", HarmVotes: 3})

	risk, _, err := session.AssessHarm(context.Background(), "", "go test ./...")
	if err != nil {
		t.Fatalf("AssessHarm: %v", err)
	}
	if harmRiskRank(risk) >= harmRiskRank("medium") {
		t.Fatalf("median of [low, low, high] = %q, want an auto-allow tier", risk)
	}
}

// Voting uses a non-zero temperature so the independent samples can actually differ.
func TestAssessHarmVotesUseNonZeroTemperature(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents(`{"harmful": false, "reason": "ok"}`)},
		fakeProviderResponse{events: textEvents(`{"harmful": false, "reason": "ok"}`)},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Model: "m", HarmVotes: 2})

	if _, _, err := session.AssessHarm(context.Background(), "", "cmd"); err != nil {
		t.Fatalf("AssessHarm: %v", err)
	}
	if tp := provider.requests[0].Temperature; tp == nil || *tp <= 0 {
		t.Fatalf("vote temperature = %v, want > 0 for sampling diversity", tp)
	}
}

// A single vote (default) keeps the original deterministic behavior: one request.
func TestAssessHarmSingleVoteByDefault(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents(`{"harmful": true, "reason": "危险"}`)},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Model: "m"})

	if _, _, err := session.AssessHarm(context.Background(), "", "cmd"); err != nil {
		t.Fatalf("AssessHarm: %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1 (single vote by default)", len(provider.requests))
	}
	if tp := provider.requests[0].Temperature; tp == nil || *tp != 0 {
		t.Fatalf("single-check temperature = %v, want 0 (deterministic)", tp)
	}
}
