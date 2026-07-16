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
