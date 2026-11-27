package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPickFileAttachmentCopiesUnderPasted(t *testing.T) {
	root := isolatePastedRoot(t)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "报告.docx")
	want := []byte("docx-bytes-\x00\xff")
	if err := os.WriteFile(src, want, 0o600); err != nil {
		t.Fatal(err)
	}

	app := &App{dialog: fakeDialoger{path: src}}
	got, err := app.PickFileAttachment()
	if err != nil {
		t.Fatalf("PickFileAttachment: %v", err)
	}
	if filepath.Base(got) != "报告.docx" {
		t.Errorf("basename = %q", filepath.Base(got))
	}
	if !strings.HasPrefix(got, root+string(filepath.Separator)) {
		t.Fatalf("path %q not under pasted root %q", got, root)
	}
	data, err := os.ReadFile(got) //nolint:gosec
	if err != nil {
		t.Fatalf("read copy: %v", err)
	}
	if string(data) != string(want) {
		t.Fatalf("copy bytes = %q, want %q", data, want)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source must remain after copy: %v", err)
	}
}

func TestPickFileAttachmentCancel(t *testing.T) {
	isolatePastedRoot(t)
	app := &App{dialog: fakeDialoger{path: ""}}
	got, err := app.PickFileAttachment()
	if err != nil {
		t.Fatalf("cancel err = %v", err)
	}
	if got != "" {
		t.Fatalf("cancel path = %q, want empty", got)
	}
}

func TestPickFileAttachmentRejectsTooLarge(t *testing.T) {
	root := isolatePastedRoot(t)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "huge.bin")
	// Sparse-ish: write past the limit without filling every byte if possible —
	// os.WriteFile of maxPastedBytes+1 is fine for the test gate.
	big := make([]byte, maxPastedBytes+1)
	if err := os.WriteFile(src, big, 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{dialog: fakeDialoger{path: src}}
	got, err := app.PickFileAttachment()
	if err == nil {
		t.Fatal("expected size error")
	}
	if got != "" {
		t.Fatalf("path on error = %q", got)
	}
	// No usable copy of this basename should land under pasted/.
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		if filepath.Base(path) == "huge.bin" {
			t.Errorf("oversized file left at %s", path)
		}
		return nil
	})
}
