package memory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/engine/tool"
)

func TestRememberToolSavesProjectByDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStore(Options{ProjectPath: filepath.Join(dir, ".runcode", "memory.md")})
	tl := NewTool(s)

	res := runRemember(t, tl, rememberInput{Fact: "the API base path is /v2"})
	if res.IsError {
		t.Fatalf("unexpected error result: %s", resultText(res))
	}
	l, _ := s.Load()
	if len(l.Project) != 1 || len(l.User) != 0 {
		t.Fatalf("fact should land in project memory: %#v", l)
	}
}

func TestRememberToolUserScope(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStore(Options{
		UserPath:    filepath.Join(dir, "user.md"),
		ProjectPath: filepath.Join(dir, "proj.md"),
	})
	res := runRemember(t, NewTool(s), rememberInput{Fact: "prefers Chinese replies", Scope: "user"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	l, _ := s.Load()
	if len(l.User) != 1 || len(l.Project) != 0 {
		t.Fatalf("fact should land in user memory: %#v", l)
	}
}

func TestRememberToolDuplicateIsNotError(t *testing.T) {
	t.Parallel()
	s := NewStore(Options{ProjectPath: filepath.Join(t.TempDir(), "memory.md")})
	tl := NewTool(s)
	runRemember(t, tl, rememberInput{Fact: "uses pnpm"})
	res := runRemember(t, tl, rememberInput{Fact: "uses pnpm"})
	if res.IsError {
		t.Fatal("a duplicate should be reported, not errored")
	}
	if !strings.Contains(strings.ToLower(resultText(res)), "already") {
		t.Fatalf("duplicate result should say already: %q", resultText(res))
	}
}

func TestRememberToolInvalidInputs(t *testing.T) {
	t.Parallel()
	tl := NewTool(NewStore(Options{ProjectPath: filepath.Join(t.TempDir(), "memory.md")}))

	if res := runRemember(t, tl, rememberInput{Fact: "   "}); !res.IsError {
		t.Fatal("blank fact should error")
	}
	if res := runRemember(t, tl, rememberInput{Fact: "x", Scope: "global"}); !res.IsError {
		t.Fatal("unknown scope should error")
	}
	bad, err := tl.Run(context.Background(), json.RawMessage("not json"), nil, nil)
	if err != nil {
		t.Fatalf("Run returned a Go error for bad json: %v", err)
	}
	if !bad.IsError {
		t.Fatal("malformed input should be an is_error result")
	}
}

func TestRememberToolUnavailableScope(t *testing.T) {
	t.Parallel()
	// Project-only store; asking to save to user scope returns an is_error result.
	tl := NewTool(NewStore(Options{ProjectPath: filepath.Join(t.TempDir(), "memory.md")}))
	res := runRemember(t, tl, rememberInput{Fact: "x", Scope: "user"})
	if !res.IsError {
		t.Fatal("saving to an unavailable scope should be an error result")
	}
}

func TestRememberToolMetadata(t *testing.T) {
	t.Parallel()
	tl := NewTool(NewStore(Options{}))
	if tl.Name() != ToolName {
		t.Fatalf("name = %q", tl.Name())
	}
	if tl.IsConcurrencySafe() {
		t.Fatal("Remember writes a file; it must not be concurrency-safe")
	}
}

func runRemember(t *testing.T, tl *Tool, in rememberInput) tool.Result {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res, err := tl.Run(context.Background(), raw, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func resultText(r tool.Result) string {
	var parts []string
	for _, c := range r.Content {
		parts = append(parts, c.Text)
	}
	return strings.Join(parts, "")
}
