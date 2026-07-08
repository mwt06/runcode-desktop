package desktop

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
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
