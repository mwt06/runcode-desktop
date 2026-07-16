// Package websearch implements the WebSearch tool: a keyless web search via
// DuckDuckGo's no-JavaScript HTML results page. It is an outbound network
// operation, so the permission layer requires approval before it runs.
package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/engine/webclient"
)

const (
	requestTimeout    = 20 * time.Second
	maxRedirects      = 5
	defaultMaxResults = 5
	hardMaxResults    = 10
	maxBodyBytes      = 4 << 20 // cap the results page we read (4 MiB)
	// ddgEndpoint is DuckDuckGo's no-JavaScript HTML results page. It needs no API
	// key: we POST the query as a form field with a browser User-Agent so the request
	// isn't refused, then scrape the result rows.
	ddgEndpoint = "https://html.duckduckgo.com/html/"
)

type input struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

// Tool is the WebSearch tool. client and endpoint are overridable so tests can point
// at an httptest server with a permissive (non-SSRF-blocking) client.
type Tool struct {
	client   *http.Client
	endpoint string
}

func New() tool.Tool {
	return Tool{client: webclient.New(requestTimeout, maxRedirects), endpoint: ddgEndpoint}
}

func (Tool) Name() string { return "WebSearch" }

func (Tool) Description() string {
	return "Search the web via DuckDuckGo and return the top results (title, URL, snippet). " +
		"No API key is required. This is a network operation and requires approval."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"query":       {Type: tool.SchemaTypeString, Description: "The search query."},
			"max_results": {Type: tool.SchemaTypeInteger, Description: "Maximum results to return (1-10, default 5)."},
		},
		Required:             []string{"query"},
		AdditionalProperties: false,
	}
}

// IsConcurrencySafe mirrors WebFetch: WebSearch holds no shared state (it only reads
// over the network) and the approver queues concurrent prompts by id, so several
// searches can run in parallel without clobbering each other's approval.
func (Tool) IsConcurrencySafe() bool { return true }

func (t Tool) Run(ctx context.Context, raw json.RawMessage, _ *tool.Context, events chan<- tool.Event) (tool.Result, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse websearch input: %w", err)
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return tool.Result{}, errors.New("query is required")
	}
	limit := in.MaxResults
	if limit <= 0 {
		limit = defaultMaxResults
	}
	if limit > hardMaxResults {
		limit = hardMaxResults
	}

	endpoint := t.endpoint
	if endpoint == "" {
		endpoint = ddgEndpoint
	}
	client := t.client
	if client == nil {
		client = webclient.New(requestTimeout, maxRedirects)
	}

	form := url.Values{"q": {query}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tool.Result{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", webclient.BrowserUserAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return tool.Result{}, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tool.Result{
			IsError: true,
			Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: fmt.Sprintf("DuckDuckGo returned HTTP %d for %q", resp.StatusCode, query)}},
		}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return tool.Result{}, fmt.Errorf("read search results: %w", err)
	}

	results, err := parseResults(body, limit)
	if err != nil {
		return tool.Result{}, fmt.Errorf("parse search results: %w", err)
	}
	if len(results) == 0 {
		return tool.Result{
			Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: fmt.Sprintf("No results found for %q.", query)}},
		}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Search results for %q:\n\n", query)
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&sb, "   %s\n", r.Snippet)
		}
		emitOutput(events, fmt.Sprintf("%d. %s — %s", i+1, r.Title, r.URL))
	}
	return tool.Result{
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: strings.TrimRight(sb.String(), "\n")}},
	}, nil
}

// parseResults scrapes DuckDuckGo's HTML result rows: each result is a title anchor
// (class result__a) whose href is a DDG redirect wrapping the real URL, optionally
// followed by a snippet element (class result__snippet). It stops once limit results
// have their title.
func parseResults(body []byte, limit int) ([]searchResult, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var results []searchResult
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			class := attr(n, "class")
			switch {
			case n.Data == "a" && strings.Contains(class, "result__a"):
				if len(results) < limit {
					if u := resultURL(attr(n, "href")); u != "" {
						if title := textContent(n); title != "" {
							results = append(results, searchResult{Title: title, URL: u})
						}
					}
				}
			case strings.Contains(class, "result__snippet"):
				if len(results) > 0 && results[len(results)-1].Snippet == "" {
					results[len(results)-1].Snippet = textContent(n)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return results, nil
}

// resultURL unwraps a DuckDuckGo result href into the real destination URL. DDG wraps
// external links as //duckduckgo.com/l/?uddg=<url-encoded target>&…; direct http(s)
// or protocol-relative links are returned as-is. Internal/relative links yield "".
func resultURL(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if i := strings.Index(href, "uddg="); i >= 0 {
		raw := href[i+len("uddg="):]
		if amp := strings.IndexByte(raw, '&'); amp >= 0 {
			raw = raw[:amp]
		}
		if decoded, err := url.QueryUnescape(raw); err == nil && strings.HasPrefix(decoded, "http") {
			return decoded
		}
	}
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	return ""
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// textContent returns the whitespace-collapsed text of a node's descendants.
func textContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(sb.String()), " ")
}

// emitOutput sends a best-effort progress line; it never blocks the search.
func emitOutput(events chan<- tool.Event, line string) {
	if events == nil {
		return
	}
	select {
	case events <- tool.Event{Type: tool.EventTypeOutput, Output: []tool.OutputLine{{Stream: tool.OutputStreamStdout, Text: line}}}:
	default:
	}
}
