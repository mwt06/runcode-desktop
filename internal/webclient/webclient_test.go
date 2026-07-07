package webclient

import (
	"net"
	"testing"
)

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
