package webclient

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// noProxyEnv clears every proxy variable so a developer's ambient proxy cannot
// steer a test down the wrong mode.
func noProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{ProxyEnvVar, "HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
	}
}

// unresolvable host: the .invalid TLD is reserved never to resolve, so these tests
// exercise proxying without depending on DNS or the network.
const blockedHost = "http://search.invalid/q"

func TestDirectClientStillBlocksNonPublicAddresses(t *testing.T) {
	noProxyEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("hi")) }))
	defer srv.Close()
	// srv is on loopback: with no proxy the dial-time guard must refuse it.
	if _, err := New(5*time.Second, 3).Get(srv.URL); err == nil {
		t.Fatal("direct client reached a loopback server, want refusal")
	}
}

func TestProxyEnvRoutesThroughProxy(t *testing.T) {
	noProxyEnv(t)
	var got string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.RequestURI // proxied requests carry the absolute URL
		w.Write([]byte("via proxy"))
	}))
	defer proxy.Close()
	t.Setenv(ProxyEnvVar, proxy.URL)

	// The proxy itself is on loopback — the dial guard must not reject it.
	resp, err := New(5*time.Second, 3).Get(blockedHost)
	if err != nil {
		t.Fatalf("proxied Get: %v", err)
	}
	defer resp.Body.Close()
	if got != blockedHost {
		t.Fatalf("proxy saw %q, want %q", got, blockedHost)
	}
}

func TestStandardProxyEnvHonored(t *testing.T) {
	noProxyEnv(t)
	hit := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.Write([]byte("ok"))
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)

	resp, err := New(5*time.Second, 3).Get(blockedHost)
	if err != nil {
		t.Fatalf("proxied Get: %v", err)
	}
	defer resp.Body.Close()
	if !hit {
		t.Fatal("HTTP_PROXY was not used")
	}
}

// The whole point of the URL-layer guard: a proxy must not become a way to reach
// addresses the direct client would refuse.
func TestProxiedClientRefusesNonPublicTargets(t *testing.T) {
	noProxyEnv(t)
	hit := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.Write([]byte("should never happen"))
	}))
	defer proxy.Close()
	t.Setenv(ProxyEnvVar, proxy.URL)

	client := New(5*time.Second, 3)
	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data/", // cloud metadata
		"http://192.168.1.1/admin",                 // intranet
		"http://127.0.0.1:8080/",                   // loopback literal
		"http://localhost:8080/",                   // loopback by name
		"http://[::1]:8080/",                       // loopback v6
	} {
		if _, err := client.Get(target); err == nil {
			t.Errorf("Get(%s) succeeded through the proxy, want refusal", target)
		}
	}
	if hit {
		t.Fatal("a non-public target reached the proxy")
	}
}

// A malformed proxy must fail the request, never silently fall back to a direct
// connection — that would leak a request the user asked to route through a proxy.
func TestInvalidProxyFailsClosed(t *testing.T) {
	noProxyEnv(t)
	t.Setenv(ProxyEnvVar, "not-a-url")
	_, err := New(5*time.Second, 3).Get(blockedHost)
	if err == nil {
		t.Fatal("invalid proxy did not fail the request")
	}
	if !strings.Contains(err.Error(), ProxyEnvVar) {
		t.Fatalf("error %v does not name %s", err, ProxyEnvVar)
	}
}

// An empty injected proxy must be byte-for-byte the env-fallback path: here the
// env proxy is honored, exactly as New would.
func TestNewWithProxyEmptyFallsBackToEnv(t *testing.T) {
	noProxyEnv(t)
	var got string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.RequestURI
		w.Write([]byte("via env proxy"))
	}))
	defer proxy.Close()
	t.Setenv(ProxyEnvVar, proxy.URL)

	resp, err := NewWithProxy(5*time.Second, 3, "").Get(blockedHost)
	if err != nil {
		t.Fatalf("proxied Get: %v", err)
	}
	defer resp.Body.Close()
	if got != blockedHost {
		t.Fatalf("env proxy saw %q, want %q", got, blockedHost)
	}
}

// And with no proxy anywhere, an empty injected proxy keeps the direct client's
// dial-time guard (loopback refused), identical to New.
func TestNewWithProxyEmptyStaysDirect(t *testing.T) {
	noProxyEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("hi")) }))
	defer srv.Close()
	if _, err := NewWithProxy(5*time.Second, 3, "").Get(srv.URL); err == nil {
		t.Fatal("empty-proxy client reached a loopback server, want refusal")
	}
}

// A non-empty injected proxy is authoritative: requests route through it and the
// environment is never consulted — two sessions in one process can point at
// different proxies without touching os.Setenv.
func TestNewWithProxyRoutesThroughInjectedProxyIgnoringEnv(t *testing.T) {
	noProxyEnv(t)
	var injectedHits, envHits int
	injected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		injectedHits++
		w.Write([]byte("via injected"))
	}))
	defer injected.Close()
	envProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		envHits++
		w.Write([]byte("via env"))
	}))
	defer envProxy.Close()
	t.Setenv(ProxyEnvVar, envProxy.URL) // must be ignored

	resp, err := NewWithProxy(5*time.Second, 3, injected.URL).Get(blockedHost)
	if err != nil {
		t.Fatalf("proxied Get: %v", err)
	}
	defer resp.Body.Close()
	if injectedHits != 1 || envHits != 0 {
		t.Fatalf("injected hits = %d, env hits = %d; want 1 and 0", injectedHits, envHits)
	}
}

// The injected-proxy client keeps the URL-layer SSRF guard: a proxy must not
// become a way to reach addresses the direct client would refuse.
func TestNewWithProxyRefusesNonPublicTargets(t *testing.T) {
	noProxyEnv(t)
	hit := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.Write([]byte("should never happen"))
	}))
	defer proxy.Close()

	client := NewWithProxy(5*time.Second, 3, proxy.URL)
	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data/", // cloud metadata
		"http://192.168.1.1/admin",                 // intranet
		"http://127.0.0.1:8080/",                   // loopback literal
		"http://localhost:8080/",                   // loopback by name
		"http://[::1]:8080/",                       // loopback v6
	} {
		if _, err := client.Get(target); err == nil {
			t.Errorf("Get(%s) succeeded through the injected proxy, want refusal", target)
		}
	}
	if hit {
		t.Fatal("a non-public target reached the injected proxy")
	}
}

// A malformed injected proxy must fail the request, never silently fall back to
// a direct connection — same fail-closed rule as the env path.
func TestNewWithProxyInvalidURLFailsClosed(t *testing.T) {
	noProxyEnv(t)
	_, err := NewWithProxy(5*time.Second, 3, "not-a-url").Get(blockedHost)
	if err == nil {
		t.Fatal("invalid injected proxy did not fail the request")
	}
	if !strings.Contains(err.Error(), "web proxy") {
		t.Fatalf("error %v does not name the injected web proxy setting", err)
	}
}

func TestIsPublicIP(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"8.8.8.8":         true,
		"1.1.1.1":         true,
		"2606:4700::1111": true,
		"127.0.0.1":       false,
		"::1":             false,
		"10.0.0.1":        false,
		"172.16.0.1":      false,
		"192.168.1.1":     false,
		"169.254.169.254": false, // cloud metadata endpoint
		"100.64.0.1":      false, // carrier-grade NAT
		"0.0.0.0":         false,
		"fe80::1":         false, // link-local
		"fc00::1":         false, // unique local
	}
	for addr, want := range cases {
		ip := net.ParseIP(addr)
		if ip == nil {
			t.Fatalf("bad test IP %q", addr)
		}
		if got := isPublicIP(ip); got != want {
			t.Errorf("isPublicIP(%s) = %t, want %t", addr, got, want)
		}
	}
}
