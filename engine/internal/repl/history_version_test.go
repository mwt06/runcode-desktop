package repl

import (
	"testing"

	"github.com/wt68/runcode/engine/llm"
)

// A turn's history commit must not clobber a /clear (ResetHistory) or /compact that
// ran after the turn read the working set: the intervening action wins.
func TestCommitHistoryDropsWhenReplacedMeanwhile(t *testing.T) {
	t.Parallel()
	session := newTestSession(t, SessionOptions{Provider: newFakeProviderSequence(), Model: "m"})
	session.setHistory([]llm.Message{userMessage("earlier")})

	_, version := session.historySnapshotVersioned() // what a turn would capture
	session.ResetHistory()                           // user hits /clear mid-turn

	applied := session.commitHistory([]llm.Message{userMessage("a"), userMessage("b")}, version)
	if applied {
		t.Fatal("commitHistory applied over a concurrent ResetHistory")
	}
	if got := len(session.History()); got != 0 {
		t.Fatalf("history len = %d, want 0 (the /clear should win)", got)
	}
}

// With nothing intervening, the commit applies normally.
func TestCommitHistoryAppliesWhenUnchanged(t *testing.T) {
	t.Parallel()
	session := newTestSession(t, SessionOptions{Provider: newFakeProviderSequence(), Model: "m"})

	_, version := session.historySnapshotVersioned()
	if !session.commitHistory([]llm.Message{userMessage("committed")}, version) {
		t.Fatal("commitHistory did not apply when the history was unchanged")
	}
	if got := len(session.History()); got != 1 {
		t.Fatalf("history len = %d, want 1", got)
	}
}
