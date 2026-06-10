package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/wt68/runcode/internal/persistence/sessions"
	"github.com/wt68/runcode/pkg/llm"
)

func seedSession(t *testing.T, workspace, id string, history []llm.Message) {
	t.Helper()
	store, err := sessions.OpenJSONL(workspace, id)
	if err != nil {
		t.Fatalf("open %s: %v", id, err)
	}
	if err := store.Append(context.Background(), history); err != nil {
		t.Fatalf("append %s: %v", id, err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("close %s: %v", id, err)
	}
}

func runSessionsCmd(t *testing.T, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := sessionsCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sessions %v: %v\noutput:\n%s", args, err, buf.String())
	}
	return buf.String()
}

func TestSessionsListAndShow(t *testing.T) {
	dir := t.TempDir()
	seedSession(t, dir, "sess_demo", []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "fix the bug"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
			{Type: llm.ContentBlockTypeText, Text: "looking"},
			{Type: llm.ContentBlockTypeToolUse, ID: "tu1", Name: "Read"},
		}},
		{Role: llm.RoleTool, Content: []llm.ContentBlock{
			{Type: llm.ContentBlockTypeToolResult, ToolUseID: "tu1", Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "package main"}}},
		}},
	})

	list := runSessionsCmd(t, "list", "--cwd", dir)
	if !strings.Contains(list, "sess_demo") || !strings.Contains(list, "fix the bug") {
		t.Fatalf("list output missing session data:\n%s", list)
	}

	// show by 1-based number resolved from the list
	show := runSessionsCmd(t, "show", "1", "--cwd", dir)
	for _, want := range []string{"user:", "fix the bug", "assistant:", "→ tool Read", "tool result: package main"} {
		if !strings.Contains(show, want) {
			t.Fatalf("show output missing %q:\n%s", want, show)
		}
	}

	// show by raw id should match
	if byID := runSessionsCmd(t, "show", "sess_demo", "--cwd", dir); byID != show {
		t.Fatalf("show by id and by number differ:\nby id:\n%s\nby number:\n%s", byID, show)
	}
}

func TestSessionsListEmpty(t *testing.T) {
	out := runSessionsCmd(t, "list", "--cwd", t.TempDir())
	if !strings.Contains(out, "No saved sessions") {
		t.Fatalf("want empty notice, got:\n%s", out)
	}
}

func TestSessionsShowUnknownNumberErrors(t *testing.T) {
	dir := t.TempDir()
	seedSession(t, dir, "sess_one", []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "hi"}}},
	})
	var buf bytes.Buffer
	cmd := sessionsCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"show", "5", "--cwd", dir})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for an out-of-range session number")
	}
}
