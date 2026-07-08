package desktop

import (
	"os"
	"os/exec"
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

func TestOpenCommandDoesNotUseShell(t *testing.T) {
	// A filename with a cmd metacharacter must be passed as inert argv, never through
	// a shell (no cmd.exe/sh). Builds the command only; does not launch it.
	mal := filepath.Join("C:\\ws", "x&calc.exe")
	for _, cmd := range []*exec.Cmd{openCommand(mal), revealCommand(mal)} {
		base := strings.ToLower(filepath.Base(cmd.Path))
		if strings.HasPrefix(base, "cmd") || base == "sh" || base == "bash" {
			t.Fatalf("command routes through a shell: %v", cmd.Args)
		}
	}
	// The open command passes the malicious path as one intact argument.
	found := false
	for _, a := range openCommand(mal).Args {
		if a == mal {
			found = true
		}
	}
	if !found {
		t.Fatalf("path not passed as a single inert arg: %v", openCommand(mal).Args)
	}
}
