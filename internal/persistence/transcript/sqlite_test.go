package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
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

func TestSQLiteSearchesToolCommands(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rec, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rec.Close(context.Background())

	base := time.Unix(1_700_000_000, 0).UTC()
	// A turn whose prose never mentions the command, only the tool call does.
	if err := rec.RecordTurn(context.Background(), TurnRecord{
		Time: base, SessionID: "sess_deploy", Model: "m",
		UserText: "ship it", AssistantText: "done",
		ToolCalls: []ToolCallSummary{{Name: "Bash", Command: "git push origin main"}},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	// A default search finds it via the indexed tool text.
	hits, err := rec.Search(SearchOptions{Query: "git push"})
	if err != nil || len(hits) != 1 || hits[0].SessionID != "sess_deploy" {
		t.Fatalf("default tool search = %+v, err=%v", hits, err)
	}
	// Tool-only search also matches and surfaces the command text.
	scoped, err := rec.Search(SearchOptions{Query: "origin", ToolOnly: true})
	if err != nil || len(scoped) != 1 || scoped[0].ToolText == "" {
		t.Fatalf("tool-only search = %+v, err=%v", scoped, err)
	}
	// Tool-only search must NOT match prose that lives only in user/assistant text.
	none, err := rec.Search(SearchOptions{Query: "ship it", ToolOnly: true})
	if err != nil || len(none) != 0 {
		t.Fatalf("tool-only prose search = %+v, err=%v; want no hits", none, err)
	}
}

func TestSQLiteSearchCJK(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rec, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rec.Close(context.Background())

	base := time.Unix(1_700_000_000, 0).UTC()
	recTurn(t, rec, "sess_cn", "m", "修复登录页面的崩溃问题", "已修复", base)

	// >=3 runes uses the FTS trigram index.
	if hits, err := rec.Search(SearchOptions{Query: "登录页"}); err != nil || len(hits) != 1 {
		t.Fatalf("CJK FTS search = %+v, err=%v", hits, err)
	}
	// <3 runes falls back to LIKE, which still matches a 2-character substring.
	if hits, err := rec.Search(SearchOptions{Query: "崩溃"}); err != nil || len(hits) != 1 {
		t.Fatalf("CJK LIKE-fallback search = %+v, err=%v", hits, err)
	}
}

func TestSQLiteMigratesV1ToV2(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path, err := sqlitePath(dir, true)
	if err != nil {
		t.Fatalf("path: %v", err)
	}

	// Build a v1 database by hand: the base turns table, version 1, one row whose
	// stored record carries a Bash command but no tool_text column or FTS index.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := db.Exec(schemaV1); err != nil {
		t.Fatalf("schema v1: %v", err)
	}
	if err := setUserVersion(db, 1); err != nil {
		t.Fatalf("set version: %v", err)
	}
	record := TurnRecord{
		SessionID: "sess_v1", Time: time.Unix(1_700_000_000, 0).UTC(), UserText: "deploy please",
		ToolCalls: []ToolCallSummary{{Name: "Bash", Command: "git push origin main"}},
	}
	data, _ := json.Marshal(record)
	if _, err := db.Exec(`INSERT INTO turns(session_id, ts, user_text, assistant_text, data) VALUES(?,?,?,?,?)`,
		record.SessionID, record.Time.UnixNano(), record.UserText, "", data); err != nil {
		t.Fatalf("insert v1 row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	// Reopening runs the v1->v2 migration: tool_text + FTS are backfilled from the
	// stored record JSON, so the command becomes searchable.
	rec, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("reopen/migrate: %v", err)
	}
	defer rec.Close(context.Background())
	hits, err := rec.Search(SearchOptions{Query: "git push"})
	if err != nil || len(hits) != 1 || hits[0].SessionID != "sess_v1" {
		t.Fatalf("post-migration tool search = %+v, err=%v", hits, err)
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
