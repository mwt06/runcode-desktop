package openai

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTapSSEDisabledReturnsSameBody(t *testing.T) {
	t.Setenv(SSEDumpDirEnv, "")
	body := io.NopCloser(strings.NewReader("data: x\n\n"))
	if got := tapSSE(body); got != body {
		t.Fatal("tapSSE returned a wrapper while capture is disabled; want the original body")
	}
}

func TestTapSSEMirrorsBytesToFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(SSEDumpDirEnv, dir)

	raw := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"
	tapped := tapSSE(io.NopCloser(strings.NewReader(raw)))
	got, err := io.ReadAll(tapped)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := tapped.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if string(got) != raw {
		t.Fatalf("read-through = %q, want pass-through of %q", got, raw)
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("dump dir entries = %v (err %v), want exactly one capture file", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read dump: %v", err)
	}
	if string(data) != raw {
		t.Fatalf("dump = %q, want the raw stream %q", data, raw)
	}
}
