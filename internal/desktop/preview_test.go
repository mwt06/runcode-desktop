package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func appWithWorkspace(t *testing.T, ws string) *App {
	t.Helper()
	a := New(&recordingSink{})
	a.workspace = ws
	return a
}

func TestReadArtifactReturnsWorkspaceText(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "a.md"), []byte("# hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := appWithWorkspace(t, ws)
	got, err := a.ReadArtifact("a.md")
	if err != nil || got != "# hi" {
		t.Fatalf("ReadArtifact = (%q, %v), want (# hi, nil)", got, err)
	}
}

func TestReadArtifactRejectsOutsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	a := appWithWorkspace(t, ws)
	if _, err := a.ReadArtifact("../../secret.txt"); err == nil {
		t.Fatal("ReadArtifact allowed a path outside the workspace")
	}
}

func TestReadArtifactRejectsTooLarge(t *testing.T) {
	ws := t.TempDir()
	big := strings.Repeat("x", maxArtifactBytes+1)
	if err := os.WriteFile(filepath.Join(ws, "big.txt"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	a := appWithWorkspace(t, ws)
	if _, err := a.ReadArtifact("big.txt"); err == nil {
		t.Fatal("ReadArtifact returned an over-sized file instead of erroring")
	}
}

func TestReadArtifactRejectsBinary(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "b.bin"), []byte{0x00, 0x01, 0xff, 0xfe}, 0o600); err != nil {
		t.Fatal(err)
	}
	a := appWithWorkspace(t, ws)
	if _, err := a.ReadArtifact("b.bin"); err == nil {
		t.Fatal("ReadArtifact returned binary content instead of erroring")
	}
}

func TestReadArtifactRejectsSymlinkEscape(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("TOP-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "escape")); err != nil {
		t.Skipf("symlinks unsupported here: %v", err)
	}
	a := appWithWorkspace(t, ws)
	if got, err := a.ReadArtifact("escape/secret.txt"); err == nil {
		t.Fatalf("symlink escape leaked outside-workspace content: %q", got)
	}
}

func TestReadArtifactRejectsJunctionEscape(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows-only")
	}
	ws := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("TOP-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(ws, "evil"), outside).CombinedOutput(); err != nil {
		t.Skipf("mklink /J unavailable: %v (%s)", err, out)
	}
	a := appWithWorkspace(t, ws)
	if got, err := a.ReadArtifact("evil/secret.txt"); err == nil {
		t.Fatalf("junction escape leaked outside-workspace content: %q", got)
	}
}
