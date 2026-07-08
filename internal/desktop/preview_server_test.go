package desktop

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func getPreview(t *testing.T, baseURL, rel string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(baseURL + rel)
	if err != nil {
		t.Fatalf("GET %s: %v", rel, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

func TestPreviewServerServesWorkspaceFile(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "index.html"), []byte("<h1>hi</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	ps := newPreviewServer()
	base, err := ps.start(ws)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ps.stop()
	if !strings.HasPrefix(base, "http://127.0.0.1:") || !strings.HasSuffix(base, "/") {
		t.Fatalf("baseURL = %q, want http://127.0.0.1:<port>/", base)
	}
	resp, body := getPreview(t, base, "index.html")
	if resp.StatusCode != 200 || body != "<h1>hi</h1>" {
		t.Fatalf("serve = %d %q, want 200 <h1>hi</h1>", resp.StatusCode, body)
	}
}

func TestPreviewServerRejectsTraversal(t *testing.T) {
	ws := t.TempDir()
	ps := newPreviewServer()
	base, _ := ps.start(ws)
	defer ps.stop()
	// %2e%2e%2f = "../" ; must not escape the workspace.
	resp, _ := getPreview(t, base, "%2e%2e%2f%2e%2e%2fwindows%2fwin.ini")
	if resp.StatusCode == 200 {
		t.Fatal("traversal request was served (should be 403/404)")
	}
}

func TestPreviewServerRejectsNonGET(t *testing.T) {
	ws := t.TempDir()
	ps := newPreviewServer()
	base, _ := ps.start(ws)
	defer ps.stop()
	resp, err := http.Post(base+"index.html", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", resp.StatusCode)
	}
}

func TestPreviewServerStopCloses(t *testing.T) {
	ws := t.TempDir()
	ps := newPreviewServer()
	base, _ := ps.start(ws)
	ps.stop()
	if _, err := http.Get(base + "index.html"); err == nil {
		t.Fatal("server still reachable after stop()")
	}
}

func TestPreviewServerServesDotDotPrefixedName(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "..hidden"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	ps := newPreviewServer()
	base, _ := ps.start(ws)
	defer ps.stop()
	resp, body := getPreview(t, base, "..hidden")
	if resp.StatusCode != 200 || body != "ok" {
		t.Fatalf("file named ..hidden should be served: %d %q", resp.StatusCode, body)
	}
}

func TestPreviewServerRejectsSymlinkEscape(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("TOP-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "escape")); err != nil {
		t.Skipf("symlinks unsupported here: %v", err)
	}
	ps := newPreviewServer()
	base, _ := ps.start(ws)
	defer ps.stop()
	resp, body := getPreview(t, base, "escape/secret.txt")
	if resp.StatusCode == 200 {
		t.Fatalf("symlink escape served an outside-workspace file: %d %q", resp.StatusCode, body)
	}
}

func TestPreviewServerServesClampedTraversal(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "foo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "bar"), []byte("BAR"), 0o600); err != nil {
		t.Fatal(err)
	}
	ps := newPreviewServer()
	base, _ := ps.start(ws)
	defer ps.stop()
	// /foo/../../bar clamps to /bar (an existing in-workspace file) and must be
	// served. Send the raw request line over TCP so the ".." segments are not
	// cleaned by the HTTP client before reaching the server.
	addr := strings.TrimPrefix(strings.TrimSuffix(base, "/"), "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET /foo/../../bar HTTP/1.0\r\nHost: %s\r\n\r\n", addr)
	resp, _ := io.ReadAll(conn)
	s := string(resp)
	if !strings.Contains(s, " 200 ") || !strings.Contains(s, "BAR") {
		t.Fatalf("clamped traversal to in-workspace file not served; response:\n%s", s)
	}
}

func TestPreviewServerRejectsJunctionEscape(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows-only")
	}
	ws := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("TOP-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	// mklink /J creates a directory junction and needs no elevation.
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(ws, "evil"), outside).CombinedOutput(); err != nil {
		t.Skipf("mklink /J unavailable: %v (%s)", err, out)
	}
	ps := newPreviewServer()
	base, _ := ps.start(ws)
	defer ps.stop()
	resp, body := getPreview(t, base, "evil/secret.txt")
	if resp.StatusCode == 200 {
		t.Fatalf("junction escape served an outside-workspace file: %d %q", resp.StatusCode, body)
	}
}
