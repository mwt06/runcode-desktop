package sessions

import (
	"context"
	"reflect"
	"testing"

	"github.com/wt68/runcode/engine/llm"
)

func userMsg(text string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: text}}}
}

func assistantMsg(text string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: text}}}
}

// TestBackendParity runs the same scenario against both backends through the
// Backend interface, so JSONL and SQLite stay behavior-compatible.
func TestBackendParity(t *testing.T) {
	for _, kind := range []string{BackendJSONL, BackendSQLite} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			backend, err := OpenBackend(dir, kind)
			if err != nil {
				t.Fatalf("OpenBackend(%s): %v", kind, err)
			}
			defer backend.Close(context.Background())

			// Two sessions; sess_b is written last so it is newest.
			seedViaBackend(t, backend, "sess_a", []llm.Message{userMsg("first task"), assistantMsg("done")})
			history := []llm.Message{userMsg("fix the bug"), assistantMsg("looking"), userMsg("now ship it")}
			// append in two batches to exercise seq continuation
			storeAppend(t, backend, "sess_b", history[:2])
			storeAppend(t, backend, "sess_b", history[2:])

			// LoadHistory round-trips exactly.
			got, err := backend.LoadHistory("sess_b")
			if err != nil {
				t.Fatalf("LoadHistory: %v", err)
			}
			if !reflect.DeepEqual(got, history) {
				t.Fatalf("history mismatch:\n got %#v\nwant %#v", got, history)
			}

			// List is newest-first with correct turn counts and previews.
			infos, err := backend.List()
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(infos) != 2 {
				t.Fatalf("got %d sessions, want 2", len(infos))
			}
			if infos[0].ID != "sess_b" || infos[1].ID != "sess_a" {
				t.Fatalf("order = %s,%s; want sess_b,sess_a", infos[0].ID, infos[1].ID)
			}
			if infos[0].Turns != 2 || infos[0].FirstUser != "fix the bug" || infos[0].LastUser != "now ship it" {
				t.Fatalf("sess_b metadata = %+v", infos[0])
			}

			// Describe and Latest.
			d, err := backend.Describe("sess_a")
			if err != nil || d.Turns != 1 || d.FirstUser != "first task" {
				t.Fatalf("Describe(sess_a) = %+v, err=%v", d, err)
			}
			if _, err := backend.Describe("sess_missing"); err == nil {
				t.Fatal("Describe of a missing session should error")
			}
			latest, err := backend.Latest()
			if err != nil || latest != "sess_b" {
				t.Fatalf("Latest = %q, err=%v; want sess_b", latest, err)
			}
		})
	}
}

func TestSQLitePersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	backend, err := OpenBackend(dir, BackendSQLite)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	storeAppend(t, backend, "sess_persist", []llm.Message{userMsg("remember me"), assistantMsg("ok")})
	if err := backend.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen the same workspace — data must survive (simulating a new process).
	reopened, err := OpenBackend(dir, BackendSQLite)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close(context.Background())
	got, err := reopened.LoadHistory("sess_persist")
	if err != nil {
		t.Fatalf("LoadHistory after reopen: %v", err)
	}
	if len(got) != 2 || llm.TextContent(got[0]) != "remember me" {
		t.Fatalf("history after reopen = %#v", got)
	}
}

func TestOpenBackendUnknownKind(t *testing.T) {
	t.Parallel()
	if _, err := OpenBackend(t.TempDir(), "postgres"); err == nil {
		t.Fatal("an unknown backend kind should error")
	}
}

func TestLoadHistoryMissingSessionIsEmpty(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{BackendJSONL, BackendSQLite} {
		backend, err := OpenBackend(t.TempDir(), kind)
		if err != nil {
			t.Fatalf("open %s: %v", kind, err)
		}
		got, err := backend.LoadHistory("sess_none")
		if err != nil || got != nil {
			t.Fatalf("%s: LoadHistory(missing) = %#v, err=%v; want nil,nil", kind, got, err)
		}
		backend.Close(context.Background())
	}
}

func seedViaBackend(t *testing.T, backend Backend, id string, history []llm.Message) {
	t.Helper()
	storeAppend(t, backend, id, history)
}

func storeAppend(t *testing.T, backend Backend, id string, messages []llm.Message) {
	t.Helper()
	store, err := backend.OpenStore(id)
	if err != nil {
		t.Fatalf("OpenStore(%s): %v", id, err)
	}
	if err := store.Append(context.Background(), messages); err != nil {
		t.Fatalf("Append(%s): %v", id, err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("store Close(%s): %v", id, err)
	}
}
