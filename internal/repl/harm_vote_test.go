package repl

import (
	"context"
	"testing"
)

// With HarmVotes > 1 the harm check is a majority vote across independent samples,
// so a single fooled "safe" verdict cannot pass an action the others flag.
func TestAssessHarmMajorityVoteHarmful(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents(`{"harmful": false, "reason": "看着没事"}`)},
		fakeProviderResponse{events: textEvents(`{"harmful": true, "reason": "会删数据"}`)},
		fakeProviderResponse{events: textEvents(`{"harmful": true, "reason": "会删数据"}`)},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Model: "m", HarmVotes: 3})

	harmful, _, err := session.AssessHarm(context.Background(), "", "rm -rf /data")
	if err != nil {
		t.Fatalf("AssessHarm: %v", err)
	}
	if !harmful {
		t.Fatal("majority (2/3) said harmful, want harmful")
	}
	if len(provider.requests) != 3 {
		t.Fatalf("provider requests = %d, want 3 (one per vote)", len(provider.requests))
	}
}

// The majority still wins the other way: a lone harmful vote does not block an
// action the majority deems safe.
func TestAssessHarmMajorityVoteSafe(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents(`{"harmful": false, "reason": "常规"}`)},
		fakeProviderResponse{events: textEvents(`{"harmful": false, "reason": "常规"}`)},
		fakeProviderResponse{events: textEvents(`{"harmful": true, "reason": "误判"}`)},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Model: "m", HarmVotes: 3})

	harmful, _, err := session.AssessHarm(context.Background(), "", "go test ./...")
	if err != nil {
		t.Fatalf("AssessHarm: %v", err)
	}
	if harmful {
		t.Fatal("majority (2/3) said safe, want not harmful")
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
