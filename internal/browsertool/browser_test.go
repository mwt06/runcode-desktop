package browsertool

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLocalURL(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"http://localhost:5050/confirm?stage=2#ok", "https://LOCALHOST/", "http://127.0.0.1:1/", "http://127.1.2.3:65535/", "http://[::1]:8080/", "http://[::ffff:127.0.0.1]:8080/"} {
		if _, err := localURL(raw); err != nil {
			t.Errorf("%q: %v", raw, err)
		}
	}
	for _, raw := range []string{"", "file:///tmp/a", "javascript:alert(1)", "data:text/html,x", "ftp://localhost/", "http://example.com/", "http://192.168.1.1/", "http://0.0.0.0/", "http://[::]/", "http://127.0.0.1.example.com/", "http://localhost./", "http://[localhost]:5050/", "http://[127.0.0.1]:5050/", "http://::1:5050/", "http://user:pass@localhost/", "http://localhost@evil.test/", "http://localhost:/", "http://localhost:0/", "http://localhost:65536/", "http://localhost:abc/", "http://[::1%25eth0]/", "http://127.1/", "http://2130706433/", "http://0x7f000001/", "http://localhost/\n", " http://localhost/", "http://localhost/%0d%0a", `http://localhost\@evil.test/`, "http://localhost/%5cfoo", "http://localhost/%zz", "http://" + strings.Repeat("x", 8192)} {
		t.Run(raw[:min(len(raw), 80)], func(t *testing.T) {
			if _, err := localURL(raw); err == nil {
				t.Fatalf("accepted %q", raw)
			}
		})
	}
}

func browserInput(url string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"url": url})
	return b
}

func TestRunChecksPortWithoutHTTPAndPreservesURL(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	want := server.URL + "/confirm?stage=2&project=hello%20world#review"
	opened := ""
	tool := New(func(_ context.Context, u string) error { opened = u; return nil })
	result, err := tool.Run(context.Background(), browserInput(want), nil, nil)
	if err != nil || opened != want {
		t.Fatalf("opened=%q err=%v", opened, err)
	}
	if requests.Load() != 0 {
		t.Fatal("preflight sent a business HTTP request")
	}
	if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "不代表页面已显示或用户已确认") {
		t.Fatalf("misleading result: %+v", result)
	}
	// localhost must also find a listener that only binds IPv4.
	want = strings.Replace(want, "127.0.0.1", "localhost", 1)
	if _, err := tool.Run(context.Background(), browserInput(want), nil, nil); err != nil || opened != want {
		t.Fatalf("localhost: %q %v", opened, err)
	}
}

func TestRunNeverOpensInvalidStoppedOrCancelledService(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedURL := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		raw  json.RawMessage
	}{
		{"remote", context.Background(), browserInput("http://example.com/")},
		{"closed", context.Background(), browserInput(closedURL)},
		{"cancelled", ctx, browserInput(closedURL)},
		{"extra-field", context.Background(), json.RawMessage(`{"url":"http://localhost/","command":"whoami"}`)},
		{"multiple-json", context.Background(), json.RawMessage(`{"url":"http://localhost/"} {}`)},
		{"malformed", context.Background(), json.RawMessage(`{"url":`)},
		{"null", context.Background(), json.RawMessage(`null`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened := false
			tool := New(func(context.Context, string) error { opened = true; return nil })
			_, err := tool.Run(tc.ctx, tc.raw, nil, nil)
			if err == nil || opened {
				t.Fatalf("opened=%v err=%v", opened, err)
			}
			if tc.name == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestOpenerFailureAndUnavailableHost(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	failure := errors.New("no browser handler")
	for _, opener := range []Opener{nil, func(context.Context, string) error { return failure }} {
		_, err := New(opener).Run(context.Background(), browserInput(server.URL), nil, nil)
		if err == nil {
			t.Fatal("reported success")
		}
		if opener != nil && !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
}

func TestReadyIPv6(t *testing.T) {
	t.Parallel()
	l, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 unavailable: %v", err)
	}
	defer l.Close()
	u, err := url.Parse("http://" + l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err := ready(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	u.Host = net.JoinHostPort("localhost", u.Port())
	if err := ready(context.Background(), u); err != nil {
		t.Fatalf("IPv6-only localhost: %v", err)
	}
}

func TestApprovalHostOmitsPrivateURLPartsAndBoundsPort(t *testing.T) {
	target, err := TargetHost(browserInput("http://LOCALHOST:" + strings.Repeat("0", 1000) + "80/private?token=secret#nonce"))
	if err != nil || target != "localhost:80" {
		t.Fatalf("target=%q err=%v", target, err)
	}
	if _, err := TargetHost(browserInput("http://remote.test/")); err == nil {
		t.Fatal("approval accepted remote target")
	}
}
