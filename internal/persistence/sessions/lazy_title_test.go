package sessions

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Opening a session must not create its history file; only the first Append does,
// so a conversation the user never sends a message to leaves no on-disk record.
func TestOpenJSONLDefersFileCreation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := OpenJSONL(dir, "sess_lazy")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	path := filepath.Join(dir, ".runcode", sessionsDirName, "sess_lazy.jsonl")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("history file created on open (stat err = %v), want deferred", err)
	}
	// A never-written session must not appear in the listing.
	if infos, err := List(dir); err != nil || len(infos) != 0 {
		t.Fatalf("List = %#v (err %v), want empty before any append", infos, err)
	}

	if err := store.Append(context.Background(), sampleHistory()[:1]); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("history file missing after append: %v", err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// Closing a never-written store is a no-op and still leaves no file.
func TestCloseWithoutAppendLeavesNoFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := OpenJSONL(dir, "sess_unused")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	path := filepath.Join(dir, ".runcode", sessionsDirName, "sess_unused.jsonl")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file exists after close-without-append (stat err = %v)", err)
	}
}

func TestSaveAndLoadTitle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	id := "sess_titled"
	// Seed a history file so the session is listable.
	store, err := OpenJSONL(dir, id)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.Append(context.Background(), sampleHistory()[:1]); err != nil {
		t.Fatalf("append: %v", err)
	}
	store.Close(context.Background())

	if err := SaveTitle(dir, id, "  打印素数脚本  \n"); err != nil {
		t.Fatalf("save title: %v", err)
	}
	got, err := LoadTitle(dir, id)
	if err != nil {
		t.Fatalf("load title: %v", err)
	}
	if got != "打印素数脚本" {
		t.Fatalf("title = %q, want collapsed/trimmed", got)
	}

	// The title surfaces in the session listing.
	infos, err := List(dir)
	if err != nil || len(infos) != 1 {
		t.Fatalf("List = %#v (err %v), want one session", infos, err)
	}
	if infos[0].Title != "打印素数脚本" {
		t.Fatalf("Info.Title = %q, want the saved title", infos[0].Title)
	}

	// Saving an empty title removes the sidecar.
	if err := SaveTitle(dir, id, "   "); err != nil {
		t.Fatalf("clear title: %v", err)
	}
	if got, _ := LoadTitle(dir, id); got != "" {
		t.Fatalf("title = %q after clear, want empty", got)
	}
}

func TestLoadTitleMissingIsEmpty(t *testing.T) {
	t.Parallel()

	got, err := LoadTitle(t.TempDir(), "sess_none")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != "" {
		t.Fatalf("title = %q, want empty for missing sidecar", got)
	}
}
