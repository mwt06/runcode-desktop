package desktop

import (
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// previewServer serves the active workspace read-only over loopback HTTP, so the
// preview panel can load HTML (with its relative assets) and images by URL. It is
// bound to 127.0.0.1 only, so no other host can reach it.
type previewServer struct {
	srv *http.Server
	ln  net.Listener
}

func newPreviewServer() *previewServer { return &previewServer{} }

// start serves workspace on 127.0.0.1:<os-assigned-port> and returns the base URL
// (ending with "/"). It is read-only (GET/HEAD) and refuses to serve outside the
// workspace.
func (p *previewServer) start(workspace string) (string, error) {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	fs := http.FileServer(http.Dir(root))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !previewPathWithinRoot(root, r.URL.Path) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		fs.ServeHTTP(w, r)
	})
	p.ln = ln
	p.srv = &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	srv := p.srv
	go func() { _ = srv.Serve(ln) }()
	return "http://" + ln.Addr().String() + "/", nil
}

func (p *previewServer) stop() {
	if p.srv != nil {
		_ = p.srv.Close()
		p.srv = nil
		p.ln = nil
	}
}

// previewPathWithinRoot reports whether the request path stays inside root,
// reusing the shared fail-closed containment check (so the static server and
// ReadArtifact enforce identical boundaries).
func previewPathWithinRoot(root, urlPath string) bool {
	_, err := resolveWithinWorkspace(root, strings.TrimPrefix(urlPath, "/"))
	return err == nil
}
