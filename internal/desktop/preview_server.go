package desktop

import (
	"net"
	"net/http"
	"path/filepath"
	"strings"
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
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fs.ServeHTTP(w, r)
	})
	p.ln = ln
	p.srv = &http.Server{Handler: handler}
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

// previewPathWithinRoot reports whether the request path stays inside root. http.Dir
// already blocks lexical ".." traversal; this additionally rejects symlink escapes.
func previewPathWithinRoot(root, urlPath string) bool {
	clean := filepath.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	full := filepath.Join(root, strings.TrimPrefix(clean, "/"))
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		// Non-existent target: http.FileServer will 404; the lexical join above
		// already cannot escape root, so allow it through.
		return true
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootResolved = root
	}
	rel, err := filepath.Rel(rootResolved, resolved)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}
