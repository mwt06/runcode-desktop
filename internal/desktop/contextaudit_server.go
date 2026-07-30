package desktop

// 上下文审核的查看服务器:只监听 127.0.0.1,提供内嵌的单文件查看页与两个只读
// JSON 接口。与 preview_server 同一套生命周期模式(系统随机端口、Close 即停),
// 额外做了 Host 头校验——审核记录里是完整提示词,比工作区静态文件更值得防
// DNS rebinding 一手。

import (
	_ "embed"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"time"
)

//go:embed contextaudit_page.html
var contextAuditPageHTML []byte

type contextAuditServer struct {
	srv *http.Server
	ln  net.Listener
}

func newContextAuditServer() *contextAuditServer { return &contextAuditServer{} }

// start serves the audit viewer on 127.0.0.1:<os-assigned-port> and returns its
// base URL. Read-only: GET/HEAD only, plus a loopback Host check.
func (s *contextAuditServer) start(store *contextAuditStore) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(contextAuditPageHTML)
	})
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, _ *http.Request) {
		sums, err := store.sessionSummaries()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// 最近活跃的排前面,查看页侧栏直接按序渲染。
		sort.Slice(sums, func(i, j int) bool { return sums[i].LastTime.After(sums[j].LastTime) })
		writeAuditJSON(w, map[string]any{"dir": store.dir, "sessions": sums})
	})
	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		records, err := store.readSession(r.URL.Query().Get("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeAuditJSON(w, map[string]any{"records": records})
	})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !loopbackHost(r.Host) {
			// 浏览器带着非回环 Host 打到本端口,只可能是 DNS rebinding 一类的把戏。
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
	s.ln = ln
	s.srv = &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	srv := s.srv
	go func() { _ = srv.Serve(ln) }()
	return "http://" + ln.Addr().String() + "/", nil
}

func (s *contextAuditServer) stop() {
	if s.srv != nil {
		_ = s.srv.Close()
		s.srv = nil
		s.ln = nil
	}
}

// loopbackHost reports whether the request's Host header names a loopback
// address (any port). 空 Host(HTTP/1.0 客户端)也放行——能连上本端口本身已经
// 说明对端在本机。
func loopbackHost(host string) bool {
	if host == "" {
		return true
	}
	h := host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		h = parsed
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func writeAuditJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
