package transcript

import (
	"context"
	"testing"
)

func TestOpenRecorderSelectsBackend(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()

	off, err := OpenRecorder("off", ws, "sess_a")
	if err != nil {
		t.Fatalf("off: %v", err)
	}
	if _, ok := off.(noopRecorder); !ok {
		t.Fatalf("off backend = %T, want noopRecorder", off)
	}

	jsonl, err := OpenRecorder(BackendJSONL, ws, "sess_a")
	if err != nil {
		t.Fatalf("jsonl: %v", err)
	}
	defer jsonl.Close(context.Background())
	if _, ok := jsonl.(*JSONLRecorder); !ok {
		t.Fatalf("jsonl backend = %T, want *JSONLRecorder", jsonl)
	}

	sqlite, err := OpenRecorder(BackendSQLite, ws, "sess_a")
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	defer sqlite.Close(context.Background())
	if _, ok := sqlite.(*SQLiteRecorder); !ok {
		t.Fatalf("sqlite backend = %T, want *SQLiteRecorder", sqlite)
	}

	if _, err := OpenRecorder("bogus", ws, "sess_a"); err == nil {
		t.Fatal("unknown backend should error")
	}
}

func TestOpenReaderAbsentThenPresent(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()

	// No transcript database yet: the reader is reported absent, not created.
	if _, ok, err := OpenReader(ws); err != nil || ok {
		t.Fatalf("OpenReader on empty workspace = ok %v, err %v; want ok=false", ok, err)
	}

	rec, err := OpenSQLite(ws)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := rec.RecordTurn(context.Background(), TurnRecord{SessionID: "s1", UserText: "hello world"}); err != nil {
		t.Fatalf("record: %v", err)
	}
	rec.Close(context.Background())

	reader, ok, err := OpenReader(ws)
	if err != nil || !ok {
		t.Fatalf("OpenReader after recording = ok %v, err %v; want ok=true", ok, err)
	}
	defer reader.Close(context.Background())
	hits, err := reader.Search(SearchOptions{Query: "hello"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].SessionID != "s1" {
		t.Fatalf("search hits = %#v, want one for s1", hits)
	}
}
