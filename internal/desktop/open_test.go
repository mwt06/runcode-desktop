package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveArtifactPathInWorkspace(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "a.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(&recordingSink{})
	a.workspace = ws
	got, err := a.ResolveArtifactPath("a.md")
	if err != nil || !strings.HasSuffix(got, "a.md") {
		t.Fatalf("ResolveArtifactPath = (%q, %v), want .../a.md, nil", got, err)
	}
}

func TestOpenBindingsRejectEscape(t *testing.T) {
	ws := t.TempDir()
	a := New(&recordingSink{})
	a.workspace = ws
	// Escaping paths must error BEFORE any OS launch — no process is started.
	if err := a.OpenExternal("../../evil.txt"); err == nil {
		t.Fatal("OpenExternal allowed an out-of-workspace path")
	}
	if err := a.RevealInFolder("../../evil.txt"); err == nil {
		t.Fatal("RevealInFolder allowed an out-of-workspace path")
	}
	if _, err := a.ResolveArtifactPath("../../evil.txt"); err == nil {
		t.Fatal("ResolveArtifactPath allowed an out-of-workspace path")
	}
}
