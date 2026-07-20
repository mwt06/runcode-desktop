package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/transcript"
)

func runTranscriptCmd(t *testing.T, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := transcriptCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("transcript %v: %v\noutput:\n%s", args, err, buf.String())
	}
	return buf.String()
}

func seedTranscript(t *testing.T, dir string) {
	t.Helper()
	rec, err := transcript.OpenSQLite(dir)
	if err != nil {
		t.Fatalf("open transcript db: %v", err)
	}
	base := time.Unix(1_700_000_000, 0).UTC()
	if err := rec.RecordTurn(context.Background(), transcript.TurnRecord{
		SessionID: "sess_x", Time: base, Model: "m", UserText: "fix the bug", AssistantText: "done",
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := rec.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestTranscriptListEmptyHint(t *testing.T) {
	isolateConfigEnv(t)
	out := runTranscriptCmd(t, "list", "--cwd", t.TempDir())
	if !strings.Contains(out, "No SQLite transcript") {
		t.Fatalf("want hint, got:\n%s", out)
	}
}

func TestTranscriptListAndSearch(t *testing.T) {
	isolateConfigEnv(t)
	dir := t.TempDir()
	seedTranscript(t, dir)

	list := runTranscriptCmd(t, "list", "--cwd", dir)
	if !strings.Contains(list, "sess_x") {
		t.Fatalf("list output:\n%s", list)
	}

	hit := runTranscriptCmd(t, "search", "bug", "--cwd", dir)
	if !strings.Contains(hit, "sess_x") || !strings.Contains(hit, "fix the bug") {
		t.Fatalf("search output:\n%s", hit)
	}

	miss := runTranscriptCmd(t, "search", "zzzznope", "--cwd", dir)
	if !strings.Contains(miss, "No turns match") {
		t.Fatalf("expected no-match notice, got:\n%s", miss)
	}
}

func TestTranscriptSearchToolFlag(t *testing.T) {
	isolateConfigEnv(t)
	dir := t.TempDir()
	rec, err := transcript.OpenSQLite(dir)
	if err != nil {
		t.Fatalf("open transcript db: %v", err)
	}
	if err := rec.RecordTurn(context.Background(), transcript.TurnRecord{
		SessionID: "sess_t", Time: time.Unix(1_700_000_000, 0).UTC(), Model: "m",
		UserText: "ship it", AssistantText: "done",
		ToolCalls: []transcript.ToolCallSummary{{Name: "Bash", Command: "git push origin main"}},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	rec.Close(context.Background())

	out := runTranscriptCmd(t, "search", "git push", "--tool", "--cwd", dir)
	if !strings.Contains(out, "sess_t") || !strings.Contains(out, "tool:") || !strings.Contains(out, "git push") {
		t.Fatalf("tool search output:\n%s", out)
	}
}

func TestTranscriptSearchDoesNotCreateDB(t *testing.T) {
	isolateConfigEnv(t)
	dir := t.TempDir()
	// Searching a workspace with no transcript DB must hint, not create one.
	out := runTranscriptCmd(t, "search", "anything", "--cwd", dir)
	if !strings.Contains(out, "No SQLite transcript") {
		t.Fatalf("want hint, got:\n%s", out)
	}
	if has, err := transcript.HasSQLite(dir); err != nil || has {
		t.Fatalf("search must not create a db: has=%v err=%v", has, err)
	}
}
