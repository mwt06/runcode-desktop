package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveWithinWorkspaceReturnsResolved(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveWithinWorkspace(ws, "a.txt")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// EvalSymlinks may canonicalize (e.g. macOS /var→/private/var); compare by suffix.
	if !strings.HasSuffix(got, "a.txt") {
		t.Fatalf("resolved = %q, want .../a.txt", got)
	}
}

func TestResolveWithinWorkspaceRejects(t *testing.T) {
	ws := t.TempDir()
	if _, err := resolveWithinWorkspace("", "a.txt"); err == nil {
		t.Fatal("empty ws should error")
	}
	if _, err := resolveWithinWorkspace(ws, "../../secret.txt"); err == nil {
		t.Fatal("lexical escape should error")
	}
	if _, err := resolveWithinWorkspace(ws, "nope.txt"); err == nil {
		t.Fatal("non-existent path should fail closed (error)")
	}
}

func TestResolveWithinWorkspaceRejectsJunctionEscape(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows-only")
	}
	ws := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(ws, "evil"), outside).CombinedOutput(); err != nil {
		t.Skipf("mklink /J unavailable: %v (%s)", err, out)
	}
	if _, err := resolveWithinWorkspace(ws, "evil/secret.txt"); err == nil {
		t.Fatal("junction escape should error (fail closed)")
	}
}
