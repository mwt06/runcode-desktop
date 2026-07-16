package transcript

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenJSONLWritesAppendOnlyRecords(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	recorder, err := OpenJSONL(workspace, "sess_test")
	if err != nil {
		t.Fatalf("OpenJSONL: %v", err)
	}
	ctx := context.Background()
	for _, text := range []string{"one", "two"} {
		if err := recorder.RecordTurn(ctx, TurnRecord{Version: 1, Type: "turn", Time: time.Unix(1, 0).UTC(), SessionID: "sess_test", UserText: text}); err != nil {
			t.Fatalf("RecordTurn: %v", err)
		}
	}
	if err := recorder.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := filepath.Join(workspace, ".runcode", "transcripts", "sess_test.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open transcript file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var records []TurnRecord
	for scanner.Scan() {
		var record TurnRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("invalid jsonl line: %v", err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan transcript: %v", err)
	}
	if len(records) != 2 || records[0].UserText != "one" || records[1].UserText != "two" {
		t.Fatalf("unexpected records: %#v", records)
	}
}

func TestOpenJSONLAppendsExistingFile(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	ctx := context.Background()
	for _, text := range []string{"one", "two"} {
		recorder, err := OpenJSONL(workspace, "sess_test")
		if err != nil {
			t.Fatalf("OpenJSONL: %v", err)
		}
		if err := recorder.RecordTurn(ctx, TurnRecord{Version: 1, Type: "turn", Time: time.Unix(1, 0).UTC(), SessionID: "sess_test", UserText: text}); err != nil {
			t.Fatalf("RecordTurn: %v", err)
		}
		if err := recorder.Close(ctx); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	data, err := os.ReadFile(filepath.Join(workspace, ".runcode", "transcripts", "sess_test.jsonl"))
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	if got := strings.Count(string(data), "\n"); got != 2 {
		t.Fatalf("line count = %d, want 2; data=%q", got, data)
	}
}

func TestOpenJSONLRejectsRuncodeSymlink(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	createSymlinkOrSkip(t, t.TempDir(), filepath.Join(workspace, ".runcode"))

	if _, err := OpenJSONL(workspace, "sess_test"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("OpenJSONL err = %v, want symlink rejection", err)
	}
}

func TestOpenJSONLRejectsTranscriptDirectorySymlink(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".runcode"), 0o700); err != nil {
		t.Fatalf("mkdir .runcode: %v", err)
	}
	createSymlinkOrSkip(t, t.TempDir(), filepath.Join(workspace, ".runcode", "transcripts"))

	if _, err := OpenJSONL(workspace, "sess_test"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("OpenJSONL err = %v, want symlink rejection", err)
	}
}

func TestOpenJSONLRejectsTranscriptFileSymlink(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	dir := filepath.Join(workspace, ".runcode", "transcripts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, nil, 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	createSymlinkOrSkip(t, outside, filepath.Join(dir, "sess_test.jsonl"))

	if _, err := OpenJSONL(workspace, "sess_test"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("OpenJSONL err = %v, want symlink rejection", err)
	}
}

func createSymlinkOrSkip(t *testing.T, oldname string, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
}

func TestValidateSessionID(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"sess_abc", "my-session.1", "A_B-9"} {
		if err := ValidateSessionID(id); err != nil {
			t.Fatalf("ValidateSessionID(%q): %v", id, err)
		}
	}
	for _, id := range []string{"", "../x", "a/b", `a\\b`, " has-space", "has space", strings.Repeat("a", 129)} {
		if err := ValidateSessionID(id); !errors.Is(err, ErrInvalidSessionID) {
			t.Fatalf("ValidateSessionID(%q) = %v, want invalid", id, err)
		}
	}
}

func TestNewSessionID(t *testing.T) {
	t.Parallel()

	first := NewSessionID()
	second := NewSessionID()
	if !strings.HasPrefix(first, "sess_") || !strings.HasPrefix(second, "sess_") || first == second {
		t.Fatalf("unexpected ids: %q %q", first, second)
	}
	if err := ValidateSessionID(first); err != nil {
		t.Fatalf("generated id is invalid: %v", err)
	}
}
