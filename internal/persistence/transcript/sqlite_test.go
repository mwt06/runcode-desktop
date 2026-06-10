package transcript

import (
	"context"
	"testing"
	"time"
)

func recTurn(t *testing.T, rec *SQLiteRecorder, sid, model, user, assistant string, when time.Time) {
	t.Helper()
	err := rec.RecordTurn(context.Background(), TurnRecord{
		Version:       1,
		Type:          "turn",
		Time:          when,
		SessionID:     sid,
		Model:         model,
		UserText:      user,
		AssistantText: assistant,
	})
	if err != nil {
		t.Fatalf("RecordTurn: %v", err)
	}
}

func TestSQLiteRecordSearchAndList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rec, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	base := time.Unix(1_700_000_000, 0).UTC()
	recTurn(t, rec, "sess_a", "m1", "fix the bug", "fixed it", base)
	recTurn(t, rec, "sess_a", "m1", "add a feature", "added", base.Add(time.Minute))
	recTurn(t, rec, "sess_b", "m2", "explore project", "explored the bug area", base.Add(2*time.Minute))

	// "bug" matches sess_a's first user text and sess_b's assistant text, newest first.
	hits, err := rec.Search(SearchOptions{Query: "bug"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}
	if hits[0].SessionID != "sess_b" || hits[1].SessionID != "sess_a" {
		t.Fatalf("hit order = %s,%s; want sess_b,sess_a", hits[0].SessionID, hits[1].SessionID)
	}

	// Scoped search.
	scoped, err := rec.Search(SearchOptions{Query: "bug", SessionID: "sess_a"})
	if err != nil || len(scoped) != 1 || scoped[0].UserText != "fix the bug" {
		t.Fatalf("scoped search = %+v, err=%v", scoped, err)
	}

	// ListSessions: sess_b most recently active, sess_a has two turns.
	digests, err := rec.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(digests) != 2 || digests[0].SessionID != "sess_b" {
		t.Fatalf("digests = %+v", digests)
	}
	if digests[1].SessionID != "sess_a" || digests[1].Turns != 2 {
		t.Fatalf("sess_a digest = %+v", digests[1])
	}

	if err := rec.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen — data persists.
	reopened, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close(context.Background())
	again, err := reopened.Search(SearchOptions{Query: "feature"})
	if err != nil || len(again) != 1 || again[0].SessionID != "sess_a" {
		t.Fatalf("search after reopen = %+v, err=%v", again, err)
	}
}

func TestSQLiteSearchEscapesLikeWildcards(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rec, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rec.Close(context.Background())

	base := time.Unix(1_700_000_000, 0).UTC()
	recTurn(t, rec, "sess_a", "m", "50% off today", "noted", base)
	recTurn(t, rec, "sess_b", "m", "no discount", "noted", base.Add(time.Minute))

	// "%" must be a literal substring, not a match-anything wildcard.
	hits, err := rec.Search(SearchOptions{Query: "%"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].SessionID != "sess_a" {
		t.Fatalf("literal %% search = %+v, want only sess_a", hits)
	}
}

func TestHasSQLite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if has, err := HasSQLite(dir); err != nil || has {
		t.Fatalf("HasSQLite on empty workspace = %v, err=%v; want false", has, err)
	}
	rec, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	rec.Close(context.Background())
	if has, err := HasSQLite(dir); err != nil || !has {
		t.Fatalf("HasSQLite after open = %v, err=%v; want true", has, err)
	}
}
