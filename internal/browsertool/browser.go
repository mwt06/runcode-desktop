// Package browsertool opens a skill's local interactive page in the user's
// browser. It does not automate the page or treat opening it as confirmation.
package browsertool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// Name is the model-facing tool name.
const Name = "open_browser"

// Opener hands a validated URL to the desktop's system browser launcher.
type Opener func(context.Context, string) error

// Tool validates a local URL before invoking the system browser.
type Tool struct{ open Opener }

// New binds the tool to a host browser opener.
func New(open Opener) tool.Tool { return Tool{open: open} }

// Name returns the wire name.
func (Tool) Name() string { return Name }

// IsConcurrencySafe prevents simultaneous focus-changing browser opens.
func (Tool) IsConcurrencySafe() bool { return false }

// InputPresentation shows the URL while its arguments are streaming.
func (Tool) InputPresentation() tool.InputPresentation {
	return tool.InputPresentation{PrimaryField: "url"}
}

// Description distinguishes opening a page from user confirmation.
func (Tool) Description() string {
	return `Open a local HTTP/HTTPS interactive page in the user's system browser, after approval. Only localhost and loopback IP addresses are accepted. Start the service first and use its actual reported URL (do not guess the port). A listening port is checked before opening; it does not prove the page is healthy. Success only means the system browser launch request was sent, not that the page rendered or the user confirmed anything. Read the skill's confirmation result separately. This is not browser automation or a browser sandbox; normal page navigation and redirects still apply. Use open_preview instead for workspace files.`
}

// InputSchema accepts only the local service URL.
func (Tool) InputSchema() tool.Schema {
	return tool.Schema{Type: tool.SchemaTypeObject, Properties: map[string]tool.Schema{
		"url": {Type: tool.SchemaTypeString, Description: "The local service's actual HTTP/HTTPS URL, including its port and any required path/query."},
	}, Required: []string{"url"}, AdditionalProperties: false}
}

// Run validates input, probes the local port, and requests a browser handoff.
func (t Tool) Run(ctx context.Context, raw json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	u, err := parse(raw)
	if err != nil {
		return tool.Result{}, err
	}
	if t.open == nil {
		return tool.Result{}, errors.New("this host cannot open a system browser")
	}
	if err = ready(ctx, u); err != nil {
		return tool.Result{}, fmt.Errorf("本地服务端口尚不可连接；请先检查启动日志和依赖，并使用服务实际报告的网址：%w", err)
	}
	if err = ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if err = t.open(ctx, u.String()); err != nil {
		return tool.Result{}, fmt.Errorf("无法请求系统浏览器打开 %s：%w", u.String(), err)
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText,
		Text: "已将打开请求交给系统浏览器：" + u.String() + "。这不代表页面已显示或用户已确认；请继续检查服务日志，并按技能约定等待用户确认结果。",
	}}}, nil
}

// TargetHost validates the input and returns only the bounded host/port for
// approval display. Query strings and paths must not enter approval telemetry.
func TargetHost(raw json.RawMessage) (string, error) {
	u, err := parse(raw)
	if err != nil {
		return "", err
	}
	return u.Host, nil
}

func parse(raw json.RawMessage) (*url.URL, error) {
	var in struct {
		URL string `json:"url"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("parse %s input: %w", Name, err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("expected one JSON object")
	}
	return localURL(in.URL)
}

func localURL(raw string) (*url.URL, error) {
	// Reject browser/parser ambiguities before passing anything to a system API.
	decoded, err := url.PathUnescape(raw)
	if err != nil || len(raw) > 8192 || strings.TrimSpace(raw) != raw || strings.ContainsAny(decoded, `\`) || strings.IndexFunc(decoded, unicode.IsControl) >= 0 {
		return nil, errors.New("invalid URL: whitespace, control characters, backslashes or excessive length")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || u.User != nil {
		return nil, errors.New("only HTTP/HTTPS URLs without embedded credentials are supported")
	}
	host := strings.ToLower(u.Hostname())
	if host != "localhost" {
		ip, err := netip.ParseAddr(host)
		if err != nil || ip.Zone() != "" || !ip.IsLoopback() {
			return nil, errors.New("only localhost and loopback IP addresses are supported")
		}
	}

	ipv6 := strings.Contains(host, ":")
	if (ipv6 && !strings.HasPrefix(u.Host, "[")) || (!ipv6 && strings.ContainsAny(u.Host, "[]")) {
		return nil, errors.New("IPv6 hosts require brackets; other hosts must not use brackets")
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, errors.New("URL port must not be empty")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("URL port must be between 1 and 65535")
		}
		// Normalize the authority for both the browser and bounded approval
		// metadata (a port can otherwise contain thousands of leading zeros).
		u.Host = net.JoinHostPort(host, strconv.Itoa(n))
	} else if ipv6 {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	return u, nil
}

func ready(ctx context.Context, u *url.URL) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	hosts := []string{u.Hostname()}
	// Probe literal loopback addresses: do not let a local hosts-file override
	// send this readiness check elsewhere. Keep the URL's origin unchanged.
	if strings.EqualFold(hosts[0], "localhost") {
		hosts = []string{"127.0.0.1", "::1"}
	}
	var last error
	for _, host := range hosts {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
		if err == nil {
			return conn.Close()
		}
		last = err
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return last
}
