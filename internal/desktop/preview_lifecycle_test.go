package desktop

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestStartPreviewServesThenStops(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "x.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(&recordingSink{})

	a.startPreview(ws)
	base := a.previewBaseURL()
	if base == "" {
		t.Fatal("previewBaseURL empty after startPreview")
	}
	resp, err := http.Get(base + "x.txt")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("serve after start = %v %v", resp, err)
	}
	resp.Body.Close()

	a.stopPreview()
	if a.previewBaseURL() != "" {
		t.Fatal("previewBaseURL not cleared after stopPreview")
	}
}
