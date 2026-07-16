package repl

import (
	"context"
	"errors"
	"testing"
)

// A turn already in progress makes a second RunTurn fail fast, instead of racing
// the running turn on shared session state. It must not even reach the provider.
func TestRunTurnRejectsWhenTurnActive(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(fakeProviderResponse{events: textEvents("done")})
	session := newTestSession(t, SessionOptions{Provider: provider, Model: "m"})
	session.turnActive.Store(true) // a turn is "in progress"

	_, err := session.RunTurn(context.Background(), "hi")
	if !errors.Is(err, ErrTurnInProgress) {
		t.Fatalf("RunTurn err = %v, want ErrTurnInProgress", err)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("a rejected turn must not reach the provider, got %d requests", len(provider.requests))
	}
}

// A completed turn releases the guard, so the next turn runs normally.
func TestRunTurnReleasesGuardForNextTurn(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents("one")},
		fakeProviderResponse{events: textEvents("two")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Model: "m"})

	if _, err := session.RunTurn(context.Background(), "a"); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if _, err := session.RunTurn(context.Background(), "b"); err != nil {
		t.Fatalf("turn 2 (guard should have released): %v", err)
	}
}
