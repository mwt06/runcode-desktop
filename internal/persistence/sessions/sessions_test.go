package sessions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wt68/runcode/pkg/llm"
)

func sampleHistory() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "read a.go"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
			{Type: llm.ContentBlockTypeThinking, Text: "inspecting"},
			{Type: llm.ContentBlockTypeToolUse, ID: "tu1", Name: "Read", Input: json.RawMessage(`{"path":"a.go"}`)},
		}},
		{Role: llm.RoleTool, Content: []llm.ContentBlock{
			{Type: llm.ContentBlockTypeToolResult, ToolUseID: "tu1", Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "package main"}}},
		}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{
			{Type: llm.ContentBlockTypeImage, Source: &llm.ImageSource{MediaType: "image/png", Data: []byte{0, 1, 2, 255, 7}}},
		}},
	}
}

func TestAppendLoadRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := OpenJSONL(dir, "sess_test")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	history := sampleHistory()
	// append in two batches to exercise incremental writes
	if err := store.Append(context.Background(), history[:2]); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := store.Append(context.Background(), history[2:]); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	loaded, err := LoadHistory(dir, "sess_test")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(loaded, history) {
		t.Fatalf("round trip mismatch:\n got %#v\nwant %#v", loaded, history)
	}
}

func TestLoadMissingReturnsNil(t *testing.T) {
	t.Parallel()

	history, err := LoadHistory(t.TempDir(), "sess_absent")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if history != nil {
		t.Fatalf("history = %#v, want nil for missing session", history)
	}
}

func TestNoopStoreDoesNotPersist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := Noop().Append(context.Background(), sampleHistory()); err != nil {
		t.Fatalf("noop append: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".runcode")); !os.IsNotExist(err) {
		t.Fatalf("noop store created files: %v", err)
	}
}

func TestLoadCorruptLineErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sessDir := filepath.Join(dir, ".runcode", sessionsDirName)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "sess_bad.jsonl"), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadHistory(dir, "sess_bad"); err == nil {
		t.Fatal("want error for corrupt session line")
	}
}

func TestLatestSessionID(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, id := range []string{"sess_a", "sess_b", "sess_c"} {
		store, err := OpenJSONL(dir, id)
		if err != nil {
			t.Fatalf("open %s: %v", id, err)
		}
		if err := store.Append(context.Background(), sampleHistory()[:1]); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
		store.Close(context.Background())
	}
	// make sess_b the most recently modified
	base := time.Now()
	sessDir := filepath.Join(dir, ".runcode", sessionsDirName)
	must := func(name string, mod time.Time) {
		path := filepath.Join(sessDir, name+".jsonl")
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}
	must("sess_a", base.Add(-2*time.Hour))
	must("sess_c", base.Add(-1*time.Hour))
	must("sess_b", base)

	latest, err := LatestSessionID(dir)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest != "sess_b" {
		t.Fatalf("latest = %q, want sess_b", latest)
	}
}

func TestLatestSessionIDEmpty(t *testing.T) {
	t.Parallel()

	latest, err := LatestSessionID(t.TempDir())
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest != "" {
		t.Fatalf("latest = %q, want empty", latest)
	}
}

func TestOpenRejectsInvalidSessionID(t *testing.T) {
	t.Parallel()

	if _, err := OpenJSONL(t.TempDir(), "../escape"); err == nil {
		t.Fatal("want error for invalid session id")
	}
}
