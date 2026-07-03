package desktop

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestListFilesReturnsWorkspaceFilesAndSkipsMetadata(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("main.go")
	write("src/api/auth.go")
	write(".runcode/sessions/sess_a.jsonl")
	write(".git/config")
	write("node_modules/pkg/index.js")

	a := &App{workspace: ws}
	files := a.ListFiles()

	if !slices.Contains(files, "main.go") || !slices.Contains(files, "src/api/auth.go") {
		t.Fatalf("files = %v, want main.go and src/api/auth.go", files)
	}
	for _, p := range files {
		for _, bad := range []string{".runcode/", ".git/", "node_modules/"} {
			if strings.HasPrefix(p, bad) {
				t.Fatalf("file list leaked metadata path %q", p)
			}
		}
	}
}
