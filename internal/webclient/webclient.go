// Package webclient provides the SSRF-guarded HTTP client and browser User-Agent
// shared by the network tools (WebFetch, WebSearch). Keeping the security-critical
// "never connect to a non-public address" logic in one place means a fix or a new
// blocked range applies to every outbound tool at once, instead of drifting between
// per-tool copies.
package webclient

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// BrowserUserAgent presents outbound requests as a mainstream desktop browser so
// sites that reject non-browser clients don't 403 the request outright.
const BrowserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// New returns an http.Client that refuses to connect to any non-public address
// (loopback, private, link-local, unspecified, multicast, CGNAT) on the initial
// request, every redirect hop, and DNS-rebinding attempts alike, and stops after
// maxRedirects hops. timeout bounds the whole request. No proxy is configured on
// purpose: a proxy would hide the real destination IP from the Control hook.
func New(timeout time.Duration, maxRedirects int) *http.Client {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
			Control:   blockNonPublicAddr,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
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
