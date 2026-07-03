// Package webfetch implements the WebFetch tool: fetch an http(s) URL and return
// its text content (HTML reduced to plain text). It is an outbound network
// operation, so the permission layer requires approval before it runs.
package webfetch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/html"

	"github.com/wt68/runcode/pkg/tool"
)

const (
	requestTimeout   = 30 * time.Second
	maxBodyBytes     = 5 << 20 // 5 MiB read cap
	maxOutputRunes   = 50000   // extracted-text cap
	maxRedirects     = 5
	maxStreamedLines = 500 // cap live output lines while a text body downloads
	// browserUserAgent presents WebFetch as a mainstream desktop browser so sites that
	// reject non-browser clients don't 403 the request outright.
	browserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
)

type input struct {
	URL string `json:"url"`
}

type Tool struct {
	client *http.Client
}

func New() tool.Tool { return Tool{client: defaultClient()} }

func defaultClient() *http.Client {
	// A custom dialer whose Control hook runs after DNS resolution but before the
	// socket connects, so it sees the concrete IP the connection would use. This
	// blocks SSRF to loopback/private/link-local ranges for the initial request,
	// every redirect hop, and DNS-rebinding tricks alike. No proxy is configured
	// on purpose: a proxy would hide the real destination IP from this check.
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
		Timeout:   requestTimeout,
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
// connect to, so checking here defeats DNS rebinding (a hostname that resolves to
// a public IP on the first lookup and a private one at connect time).
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

func (Tool) Name() string { return "WebFetch" }

func (Tool) Description() string {
	return "Fetch an http(s) URL and return its text content; HTML is reduced to plain text. " +
		"This is a network operation and requires approval."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"url": {Type: tool.SchemaTypeString, Description: "The http(s) URL to fetch."},
		},
		Required:             []string{"url"},
		AdditionalProperties: false,
	}
}

func (Tool) IsConcurrencySafe() bool { return false }

func (t Tool) Run(ctx context.Context, raw json.RawMessage, _ *tool.Context, events chan<- tool.Event) (tool.Result, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse webfetch input: %w", err)
	}
	target := strings.TrimSpace(in.URL)
	if target == "" {
		return tool.Result{}, errors.New("url is required")
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return tool.Result{}, fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return tool.Result{}, fmt.Errorf("unsupported url scheme %q (only http and https)", parsed.Scheme)
	}

	client := t.client
	if client == nil {
		client = defaultClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return tool.Result{}, fmt.Errorf("build request: %w", err)
	}
	// Present as a real browser: many sites reject the default Go client UA (or an
	// empty one) with a 403. This gets past simple UA-based blocking; it does NOT
	// defeat JS challenges, CAPTCHAs, or bot shields like Cloudflare — those need a
	// headless browser or a search API. Accept-Encoding is intentionally left unset so
	// net/http transparently negotiates and decompresses gzip.
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,application/json;q=0.8,*/*;q=0.7")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	emitStatus(events, "正在连接 "+parsed.Host+" …")
	resp, err := client.Do(req)
	if err != nil {
		return tool.Result{}, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tool.Result{
			Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: fmt.Sprintf("HTTP %d fetching %s", resp.StatusCode, parsed.Host)}},
			IsError: true,
		}, nil
	}

	contentType := resp.Header.Get("Content-Type")
	// Text-like bodies (plain/JSON/XML) are the final content, so stream them line by
	// line as they download. HTML is post-processed into plain text from the complete
	// DOM, so it can't stream usefully — show a parse status instead; the completed
	// event then carries the extracted text.
	streamable := isStreamableContentType(contentType)
	if !streamable {
		emitStatus(events, "正在抓取并解析 "+parsed.Host+" …")
	}
	body, err := readBody(ctx, resp.Body, streamable, events)
	if err != nil {
		return tool.Result{}, fmt.Errorf("read body: %w", err)
	}
	text, err := extractText(contentType, body)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: truncateRunes(text, maxOutputRunes)}}}, nil
}

// isStreamableContentType reports whether a body is its own final content (so it can
// be streamed as it downloads) rather than HTML that needs whole-DOM extraction.
func isStreamableContentType(contentType string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if strings.Contains(mediaType, "html") {
		return false
	}
	return mediaType == "" || strings.HasPrefix(mediaType, "text/") ||
		strings.Contains(mediaType, "json") || strings.Contains(mediaType, "xml")
}

// readBody reads the response body (capped at maxBodyBytes). When stream is set it
// also emits complete lines as live output events so the UI shows content arriving;
// it always returns the full bytes for post-processing.
func readBody(ctx context.Context, r io.Reader, stream bool, events chan<- tool.Event) ([]byte, error) {
	limited := io.LimitReader(r, maxBodyBytes)
	if !stream || events == nil {
		return io.ReadAll(limited)
	}
	var buf bytes.Buffer
	var pending []byte
	chunk := make([]byte, 16<<10)
	lines := 0
	for {
		n, readErr := limited.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
			pending = append(pending, chunk[:n]...)
			var batch []tool.OutputLine
			for lines < maxStreamedLines {
				i := bytes.IndexByte(pending, '\n')
				if i < 0 {
					break
				}
				batch = append(batch, tool.OutputLine{Stream: tool.OutputStreamStdout, Text: strings.TrimRight(string(pending[:i]), "\r")})
				pending = pending[i+1:]
				lines++
			}
			if len(batch) > 0 {
				sendOutput(events, batch)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return buf.Bytes(), readErr
		}
		if ctx.Err() != nil {
			return buf.Bytes(), ctx.Err()
		}
	}
	if len(pending) > 0 && lines < maxStreamedLines {
		sendOutput(events, []tool.OutputLine{{Stream: tool.OutputStreamStdout, Text: string(pending)}})
	}
	return buf.Bytes(), nil
}

// emitStatus sends a single info line describing fetch progress (non-blocking).
func emitStatus(events chan<- tool.Event, text string) {
	sendOutput(events, []tool.OutputLine{{Stream: tool.OutputStreamInfo, Text: text}})
}

func sendOutput(events chan<- tool.Event, lines []tool.OutputLine) {
	if events == nil {
		return
	}
	select {
	case events <- tool.Event{Type: tool.EventTypeProgress, ToolName: "WebFetch", Output: lines}:
	default:
	}
}

func extractText(contentType string, body []byte) (string, error) {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	switch {
	case strings.Contains(mediaType, "html"):
		return htmlToText(body), nil
	case mediaType == "",
		strings.HasPrefix(mediaType, "text/"),
		strings.Contains(mediaType, "json"),
		strings.Contains(mediaType, "xml"):
		return string(body), nil
	default:
		return "", fmt.Errorf("unsupported content type %q", mediaType)
	}
}

var skipElements = map[string]bool{"script": true, "style": true, "noscript": true, "head": true}

var blockElements = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "tr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"section": true, "article": true, "header": true, "footer": true,
}

func htmlToText(body []byte) string {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return string(body)
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && skipElements[n.Data] {
			return
		}
		if n.Type == html.TextNode {
			if text := strings.TrimSpace(n.Data); text != "" {
				b.WriteString(text)
				b.WriteString(" ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && blockElements[n.Data] {
			b.WriteString("\n")
		}
	}
	walk(doc)
	return collapseWhitespace(b.String())
}

func collapseWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		cleaned = append(cleaned, strings.Join(strings.Fields(line), " "))
	}
	result := strings.Join(cleaned, "\n")
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result)
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "\n[output truncated]"
}
