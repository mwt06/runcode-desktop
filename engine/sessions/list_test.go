package sessions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

func writeSession(t *testing.T, workspace, id string, history []llm.Message, modTime time.Time) {
	t.Helper()
	store, err := OpenJSONL(workspace, id)
	if err != nil {
		t.Fatalf("open %s: %v", id, err)
	}
	if err := store.Append(context.Background(), history); err != nil {
		t.Fatalf("append %s: %v", id, err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("close %s: %v", id, err)
	}
	path := filepath.Join(workspace, ".runcode", "sessions", id+".jsonl")
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", id, err)
	}
}

func TestListReturnsNewestFirstWithMetadata(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	older := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "first task"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "ok"}}},
	}
	newer := sampleHistory() // two user messages, one of which is image-only (no text)
	writeSession(t, dir, "sess_old", older, time.Now().Add(-2*time.Hour))
	writeSession(t, dir, "sess_new", newer, time.Now())

	infos, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("got %d sessions, want 2", len(infos))
	}
	if infos[0].ID != "sess_new" || infos[1].ID != "sess_old" {
		t.Fatalf("order = %s,%s; want sess_new,sess_old (newest first)", infos[0].ID, infos[1].ID)
	}

	old := infos[1]
	if old.Turns != 1 || old.FirstUser != "first task" || old.LastUser != "first task" {
		t.Fatalf("old session metadata = %+v", old)
	}

	// sampleHistory has a text user prompt and an image-only user message; only the
	// text one counts as a turn/prompt.
	newInfo := infos[0]
	if newInfo.Turns != 1 || newInfo.FirstUser != "read a.go" {
		t.Fatalf("new session metadata = %+v", newInfo)
	}
	if newInfo.Messages != len(newer) {
		t.Fatalf("Messages = %d, want %d", newInfo.Messages, len(newer))
	}
}

func TestListEmptyWorkspace(t *testing.T) {
	t.Parallel()
	infos, err := List(t.TempDir())
	if err != nil {
		t.Fatalf("List on empty workspace: %v", err)
	}
	if infos != nil {
		t.Fatalf("want nil, got %#v", infos)
	}
}

func TestDescribeMissingSessionErrors(t *testing.T) {
	t.Parallel()
	if _, err := Describe(t.TempDir(), "sess_missing"); err == nil {
		t.Fatal("Describe of a missing session should error")
	}
}

func TestPreviewTextCollapsesAndTruncates(t *testing.T) {
	t.Parallel()
	if got := previewText("  hello\n\tworld  "); got != "hello world" {
		t.Fatalf("collapse = %q", got)
	}
	long := strings.Repeat("中", previewRunes+10)
	got := previewText(long)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis, got %q", got)
	}
	if n := len([]rune(strings.TrimSuffix(got, "…"))); n != previewRunes {
		t.Fatalf("truncated to %d runes, want %d", n, previewRunes)
	}
}
