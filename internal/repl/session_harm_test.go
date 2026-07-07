package repl

import (
	"context"
	"testing"
)

// The harm gate's verdict is tiny JSON, but a model sometimes wraps it in prose or a
// code fence. AssessHarm retries once with a stricter instruction so a stray prose
// reply doesn't drop a safe action to a prompt ("smart mode keeps asking me").
func TestAssessHarmRetriesWhenReplyIsNotCleanJSON(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents("Sure — this is a routine build command, totally fine.")},
		fakeProviderResponse{events: textEvents(`{"harmful": false, "reason": "常规构建命令"}`)},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Model: "mock-model"})

	harmful, reason, err := session.AssessHarm(context.Background(), "", "Run this shell command: go test ./...")
	if err != nil {
		t.Fatalf("AssessHarm: %v (the retry should have recovered)", err)
	}
	if harmful {
		t.Fatal("harmful = true, want false for a safe command")
	}
	if reason != "常规构建命令" {
		t.Fatalf("reason = %q, want 常规构建命令", reason)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("provider requests = %d, want 2 (one retry)", len(provider.requests))
	}
}

func TestAssessHarmNoRetryWhenFirstReplyIsClean(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents(`{"harmful": true, "reason": "会删除数据"}`)},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Model: "mock-model"})

	harmful, reason, err := session.AssessHarm(context.Background(), "", "Run this shell command: rm -rf /data")
	if err != nil {
		t.Fatalf("AssessHarm: %v", err)
	}
	if !harmful || reason != "会删除数据" {
		t.Fatalf("verdict = (%v, %q), want (true, 会删除数据)", harmful, reason)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1 (no retry needed)", len(provider.requests))
	}
}

// Two unparseable replies must still yield an error, so the caller falls back to a
// prompt — the fail-safe posture is preserved.
func TestAssessHarmStillFailsSafeAfterRetry(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents("no json here")},
		fakeProviderResponse{events: textEvents("still no json")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Model: "mock-model"})

	if _, _, err := session.AssessHarm(context.Background(), "", "Run this shell command: weird"); err == nil {
		t.Fatal("AssessHarm should error after both attempts fail, so the gate falls back to a prompt")
	}
}
