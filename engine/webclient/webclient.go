// Package webclient provides the SSRF-guarded HTTP client and browser User-Agent
// shared by the network tools (WebFetch, WebSearch). Keeping the security-critical
// "never connect to a non-public address" logic in one place means a fix or a new
// blocked range applies to every outbound tool at once, instead of drifting between
// per-tool copies.
package webclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// ProxyEnvVar names a proxy used only by the web tools (WebFetch/WebSearch). It
// takes precedence over the standard variables so these tools can be sent through
// a proxy without redirecting the LLM/API traffic HTTPS_PROXY also governs — the
// common case being a search endpoint that is unreachable without one.
const ProxyEnvVar = "RUNCODE_WEB_PROXY"

// resolveTimeout bounds the guard's hostname lookup in proxied mode so a slow or
// blackholed resolver cannot stall a request past the client's own timeout.
const resolveTimeout = 5 * time.Second

// BrowserUserAgent presents outbound requests as a mainstream desktop browser so
// sites that reject non-browser clients don't 403 the request outright.
const BrowserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// New returns an http.Client that refuses to connect to any non-public address
// (loopback, private, link-local, unspecified, multicast, CGNAT) on the initial
// request, every redirect hop, and DNS-rebinding attempts alike, and stops after
// maxRedirects hops. timeout bounds the whole request.
//
// Direct (the default) and proxied mode enforce that guarantee at different
// layers, because a proxy fundamentally changes what the client can observe:
//
//   - Direct: the guard is the dialer's Control hook, which sees the resolved
//     destination IP of every connection — including each redirect and reconnect —
//     so it also defeats DNS rebinding (a host that resolves public on lookup and
//     private at connect time).
//   - Proxied: every dial goes to the proxy instead, so the hook can no longer see
//     the destination, and would reject the proxy itself, which is routinely on
//     loopback. The guard therefore moves up to the URL layer (see hostGuard),
//     which is checked before each request and redirect leaves.
//
// A proxy is opt-in via ProxyEnvVar or the standard HTTPS_PROXY/HTTP_PROXY
// (honoring NO_PROXY); with none set, behavior is exactly as before.
func New(timeout time.Duration, maxRedirects int) *http.Client {
	return newClient(timeout, maxRedirects, proxyFromEnv)
}

// NewWithProxy is New with an explicitly injected proxy URL instead of the
// process environment, so concurrent sessions in one process can each use their
// own proxy (or none) without racing on os.Setenv.
//
// An empty proxyURL behaves exactly like New — including the
// RUNCODE_WEB_PROXY/HTTPS_PROXY environment fallback, which is the backward-
// compatible path for hosts that still configure the proxy via env. A non-empty
// proxyURL selects proxied mode outright (URL-layer hostGuard SSRF protection,
// identical to an env-configured proxy) and never reads the environment.
func NewWithProxy(timeout time.Duration, maxRedirects int, proxyURL string) *http.Client {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return New(timeout, maxRedirects)
	}
	return newClient(timeout, maxRedirects, func() (func(*http.Request) (*url.URL, error), bool) {
		return explicitProxy(proxyURL, "web proxy"), true
	})
}

// newClient builds the guarded client shared by New and NewWithProxy.
// resolveProxy supplies the proxy selection (and whether one is in effect);
// everything else — the dual-layer SSRF guard, timeouts, redirect policy — is
// identical in both construction paths.
func newClient(timeout time.Duration, maxRedirects int, resolveProxy func() (func(*http.Request) (*url.URL, error), bool)) *http.Client {
	proxy, proxied := resolveProxy()
	// Control only guards addresses this client dials itself. In proxied mode that
	// is the proxy, not the destination, so the check belongs at the URL layer.
	var control func(string, string, syscall.RawConn) error
	if !proxied {
		control = blockNonPublicAddr
	}
	transport := &http.Transport{
		Proxy: proxy,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
			Control:   control,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	var rt http.RoundTripper = transport
	if proxied {
		rt = hostGuard{base: transport}
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: rt,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("refusing redirect to %q scheme", req.URL.Scheme)
			}
			return nil
		},
	}
}

// proxyFromEnv resolves the proxy for the web tools from the environment and
// reports whether one is set. The environment is read on every call, unlike
// http.ProxyFromEnvironment, which caches it process-wide at first use — a host
// that sets the proxy after startup (the desktop applying a saved setting) would
// otherwise never take effect.
func proxyFromEnv() (func(*http.Request) (*url.URL, error), bool) {
	if raw := strings.TrimSpace(os.Getenv(ProxyEnvVar)); raw != "" {
		return explicitProxy(raw, ProxyEnvVar), true
	}
	cfg := httpproxy.FromEnvironment() // HTTPS_PROXY / HTTP_PROXY / NO_PROXY
	if cfg.HTTPProxy == "" && cfg.HTTPSProxy == "" {
		return nil, false
	}
	f := cfg.ProxyFunc()
	return func(req *http.Request) (*url.URL, error) { return f(req.URL) }, true
}

// explicitProxy turns a user-supplied proxy URL into a proxy func. A malformed
// URL yields a func that fails every request: falling back to a direct
// connection would quietly ignore an explicit instruction to proxy — and, for a
// user who set it to reach a blocked endpoint, leak the request straight out.
// source names where the URL came from (the env var or an injected setting) so
// the error points at the right knob.
func explicitProxy(raw, source string) func(*http.Request) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return func(*http.Request) (*url.URL, error) {
			return nil, fmt.Errorf("invalid %s %q: want a URL like http://127.0.0.1:7890", source, raw)
		}
	}
	return http.ProxyURL(u)
}

// hostGuard enforces the non-public-address rule at the URL layer, standing in for
// the dial-time check when a proxy is in front. The http.Client calls RoundTrip
// once per hop, so this covers the initial request and every redirect.
type hostGuard struct{ base http.RoundTripper }

func (g hostGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := checkPublicHost(req.Context(), req.URL); err != nil {
		return nil, err
	}
	return g.base.RoundTrip(req)
}

// checkPublicHost refuses a URL aimed at a non-public address. An IP literal is
// checked outright; a hostname is resolved and every answer must be public.
//
// A lookup failure is deliberately not a rejection. A proxy is normally configured
// precisely because the local network cannot resolve or reach the target, and the
// proxy resolves on its own — so treating "cannot resolve" as "refuse" would break
// the one case proxying exists to serve. The residual gap is a hostname that only
// resolves to a private address from the proxy's vantage point; the proxy is
// user-configured and trusted that far, and IP-literal targets — the vector that
// matters for cloud metadata and intranet endpoints — are still blocked outright.
func checkPublicHost(ctx context.Context, u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("refusing request to %q scheme", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("refusing request with no host: %s", u.Redacted())
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("refusing to connect to non-public address %s", ip)
		}
		return nil
	}
	// localhost is guaranteed loopback and may not reach the resolver at all.
	if lower := strings.ToLower(host); lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return fmt.Errorf("refusing to connect to non-public host %q", host)
	}
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil // unresolvable locally — let the proxy resolve it (see above)
	}
	for _, a := range ips {
		if !isPublicIP(a.IP) {
			return fmt.Errorf("refusing to connect to non-public address %s (%s)", a.IP, host)
		}
	}
	return nil
}

// blockNonPublicAddr is a net.Dialer Control hook that refuses connections to any
// non-public address. It receives the resolved "host:port" the socket is about to
// connect to, so checking here defeats DNS rebinding (a hostname that resolves to a
// public IP on the first lookup and a private one at connect time).
func blockNonPublicAddr(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("refusing to connect to unresolved host %q", host)
	}
	if !isPublicIP(ip) {
		return fmt.Errorf("refusing to connect to non-public address %s", ip)
	}
	return nil
}

// isPublicIP reports whether ip is a globally routable unicast address, excluding
// loopback, private (RFC 1918 / ULA), link-local, unspecified, multicast, and the
// IPv4 carrier-grade NAT range (100.64.0.0/10).
func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return false
	}
	return true
}
