// Package websearchtool implements the desktop's WebSearch tool: instead of the
// engine's keyless DuckDuckGo scraper it queries the platform's own search
// service (AI.Core 的 /v1/websearch) through the Bridge, with the signed-in
// user's Passport token.
//
// Why replace rather than add: 会话内工具名唯一，"再加一个 WebSearch" 是加不进去
// 的——它经 engine.Options.WebSearchTool 顶掉内置那个(见引擎 build.go 的
// replaceWebSearch)。保住 websearch.ToolName 这个名字是硬要求，权限归类、关闭
// 名单、子代理工具快照三处都按它寻址。
//
// 没登录通行证的连接(自填端点/自定义模型)不装它，内置的 DuckDuckGo 照旧——
// 这条工具要的是 Bridge 与用户令牌，两样都没有时它没法工作。
package websearchtool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/netorigin"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools/websearch"
)

const (
	// DefaultModel 是不另行指定时用的搜索模型。**它是这个默认值的唯一出处**：
	// Bridge 只做转发，不替客户端填模型，所以改这里就是改默认。
	DefaultModel = "al-websearch"

	defaultTimeout    = 30 * time.Second
	defaultMaxResults = 5
	// hardMaxResults 与内置 WebSearch 一致(1-10)，模型对这个工具的预期不因换后端
	// 而变；服务端另有自己的 MaxCount 上限，最终取两者更小的那个。
	hardMaxResults = 10
	maxBodyBytes   = 4 << 20
	// maxErrorText 截断上游错误正文：AI.Core 出错时回的是纯文本(有时是整段异常)，
	// 原样塞进工具结果既刷屏又白烧上下文。
	maxErrorText = 512
)

// Outcome is the terminal result of one submitted logical search, including its
// optional authentication retry. It does not infer submission from an error code.
type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
	OutcomeCancelled Outcome = "cancelled"
)

// Config 是这条工具的接线。Endpoint 与 Token 必填。
type Config struct {
	// Endpoint 是搜索端点的完整 URL(如 https://bridge/…/v1/websearch)。
	Endpoint string
	// Model 是搜索模型；留空用 DefaultModel。
	Model string
	// Token 每次调用前取一枚可用的访问令牌(与引擎的 TokenSource 同一个函数)，
	// 由它负责临期续期与并发去重。
	Token func() (string, error)
	// OnUnauthorized 在服务端回 401 时调用一次以强制刷新令牌，随后本次调用重试
	// 一次。nil 表示不重试。与引擎对 LLM 请求的处理同一套语义。
	OnUnauthorized func()
	// Client 是发请求用的 HTTP 客户端。nil 用一个带超时的普通客户端——**不能**用
	// 引擎那个加固客户端：它拒连回环/内网地址，而 Bridge 常部署在内网。
	Client *http.Client
	// Capture runs immediately before the first HTTP submission. The returned end
	// receives an explicit outcome once, including any internal 401 refresh retry.
	// Token, request construction and cancellation failures before submission do
	// not enter capture. errCode is a safe short code.
	Capture func(ctx context.Context, model string) (end func(Outcome, string))
}

// Tool 是走平台搜索服务的 WebSearch 工具。
type Tool struct {
	cfg Config
}

// New 按 cfg 造一条 WebSearch 工具。Endpoint 或 Token 缺一则返回 nil，调用方据此
// 退回内置的 WebSearch——宁可少一层替换，也不装一条注定每次都失败的工具。
func New(cfg Config) tool.Tool {
	if strings.TrimSpace(cfg.Endpoint) == "" || cfg.Token == nil {
		return nil
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = DefaultModel
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: defaultTimeout}
	}
	cfg.Client = netorigin.Restrict(cfg.Client)
	return Tool{cfg: cfg}
}

// AuthorizationBinding exposes the Bridge destination, never a fictitious search host.
func (t Tool) AuthorizationBinding(json.RawMessage) (tool.AuthorizationBinding, error) {
	origin, err := netorigin.Parse(t.cfg.Endpoint)
	return tool.AuthorizationBinding{Kind: "platform-search", Endpoint: t.cfg.Endpoint, Identity: tool.BindingFingerprint("platform-search", origin)}, err
}

// Name 返回 "WebSearch"——必须与内置同名，否则引擎在装配时就会拒掉。
func (Tool) Name() string { return websearch.ToolName }

// Description 告诉模型这条工具返回什么。刻意与内置那条保持同一形状(标题/网址/
// 摘要、需要授权)，补上来源与时间，以及区别于本机公网工具的服务端排障边界。
func (Tool) Description() string {
	return "Search the web and return the top results (title, URL, summary, source site and " +
		"publish time when available). This is a network operation and requires approval." + `

Network troubleshooting: this is platform search through the Bridge; the upstream web search runs on the server, not on this computer. The desktop web-proxy setting and RUNCODE_WEB_PROXY do not control this replacement tool, and a local proxy cannot fix server-side outbound connectivity.
On failure, do not blindly repeat requests or assume regional blocking. Distinguish client-to-Bridge DNS/connection/timeout failures from HTTP 401/403 (account/access), 429 (rate limit), and server/upstream failures (including 5xx or a reported upstream timeout). If the failing hop is unclear, say so. For client connectivity, use only bounded, read-only local DNS/proxy/listener checks with credentials redacted; do not change system settings. Inspect a configured proxy or NO_PROXY mismatch for an intranet Bridge, but never copy Passport tokens into shell commands or route Bridge/OA requests through a discovered public-web proxy. For server/upstream failures, ask the platform administrator to check the service and its outbound network; local port probing does not verify the server's network. Do not disable TLS verification or bypass approval, cancellation, disabled tools, or session data-access restrictions.
Report the failed stage, checks actually performed, uncertainty, and an actionable next step in the user's language (network administrator for connectivity, account/access check for 401/403, retry later for 429, platform administrator for server/upstream faults). If an alternative public source would help, use it only when the user permits it and session restrictions allow it, with no private query/context; label it as a fallback, not a successful platform search.`
}

// InputSchema 与内置 WebSearch 完全一致：必填 query，可选 max_results(1-10，默认 5)。
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

// IsConcurrencySafe 报 true：一次搜索只读网络，不碰共享状态。
func (Tool) IsConcurrencySafe() bool { return true }

type input struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
}

// searchRequest 是 AI.Core /v1/websearch 的请求体。字段名按服务端契约。
type searchRequest struct {
	Model   string `json:"model"`
	Query   string `json:"query"`
	Count   int    `json:"count"`
	Summary bool   `json:"summary"`
}

// searchResult 是一条搜索结果，与服务端 ResponseCreateWebSearchResultValue 对应。
type searchResult struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Summary     string `json:"summary"`
	SiteName    string `json:"siteName"`
	PublishTime string `json:"publish_time"`
}

// searchEnvelope 是服务端的统一返回信封(APIReturnInfo<T>)：State=false 表示业务
// 失败，原因在 Message。Go 解 JSON 时字段名大小写不敏感，所以服务端哪天改成小写
// 驼峰也照样解得开。
type searchEnvelope struct {
	State   bool   `json:"State"`
	Message string `json:"Message"`
	Data    struct {
		Result []searchResult `json:"result"`
	} `json:"Data"`
}

// Run 向平台搜索服务发一次查询并把结果排成编号清单，同时逐条发进度事件。
//
// 错误分两类，与内置 WebSearch 同一口径：传输/编解码失败是 Go error(工具本身没跑
// 成)；服务端明确回绝(HTTP 非 2xx、State=false)与"没搜到"是 IsError/普通结果——
// 那是搜索的结果，不是工具的故障，模型看得懂，也能换个说法再试。
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

	body, err := json.Marshal(searchRequest{Model: t.cfg.Model, Query: query, Count: limit, Summary: true})
	if err != nil {
		return tool.Result{}, fmt.Errorf("build search request: %w", err)
	}

	endCapture := func(Outcome, string) {}
	captured := false
	beforeSend := func() {
		if !captured && t.cfg.Capture != nil {
			captured = true
			if end := t.cfg.Capture(ctx, t.cfg.Model); end != nil {
				endCapture = end
			}
		}
	}

	status, payload, err := t.post(ctx, body, beforeSend)
	if err != nil {
		outcome, code := OutcomeFailed, "transport"
		if errors.Is(err, context.Canceled) {
			outcome, code = OutcomeCancelled, "canceled"
		}
		endCapture(outcome, code)
		return tool.Result{}, err
	}
	if status < 200 || status >= 300 {
		endCapture(OutcomeFailed, "http_error")
		return errorResult(fmt.Sprintf("联网搜索服务返回 HTTP %d：%s", status, upstreamMessage(payload))), nil
	}

	var env searchEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		endCapture(OutcomeFailed, "parse_error")
		return tool.Result{}, fmt.Errorf("parse search results: %w", err)
	}
	if !env.State {
		endCapture(OutcomeFailed, "upstream_reject")
		return errorResult("联网搜索失败：" + fallback(strings.TrimSpace(env.Message), "服务端未说明原因")), nil
	}

	results := env.Data.Result
	if len(results) > limit {
		results = results[:limit]
	}
	if len(results) == 0 {
		endCapture(OutcomeSucceeded, "")
		return tool.Result{
			Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: fmt.Sprintf("No results found for %q.", query)}},
		}, nil
	}
	endCapture(OutcomeSucceeded, "")
	return tool.Result{
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: format(query, results, events)}},
	}, nil
}

// post 发一次请求并读回状态码与正文。401 时先请调用方强制刷新令牌，再重试一次
// ——桌面会话可以开很久，访问令牌过期是常态而不是异常。
func (t Tool) post(ctx context.Context, body []byte, beforeSend func()) (int, []byte, error) {
	status, payload, err := t.postOnce(ctx, body, beforeSend)
	if err != nil || status != http.StatusUnauthorized || t.cfg.OnUnauthorized == nil {
		return status, payload, err
	}
	t.cfg.OnUnauthorized()
	return t.postOnce(ctx, body, beforeSend)
}

func (t Tool) postOnce(ctx context.Context, body []byte, beforeSend func()) (int, []byte, error) {
	token, err := t.cfg.Token()
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("build search request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	beforeSend()
	resp, err := t.cfg.Client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("search request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("read search results: %w", err)
	}
	return resp.StatusCode, payload, nil
}

// format 把结果排成模型好读的编号清单，并顺带逐条发一行进度(尽力而为，不阻塞)。
func format(query string, results []searchResult, events chan<- tool.Event) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Search results for %q:\n\n", query)
	for i, r := range results {
		title := fallback(collapse(r.Name), "(untitled)")
		url := strings.TrimSpace(r.URL)
		fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, title, url)
		if s := collapse(r.Summary); s != "" {
			fmt.Fprintf(&sb, "   %s\n", s)
		}
		if src := sourceLine(r); src != "" {
			fmt.Fprintf(&sb, "   %s\n", src)
		}
		emitOutput(events, fmt.Sprintf("%d. %s — %s", i+1, title, url))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// sourceLine 拼出"来源 · 时间"那一行；两者都空则不占一行。
func sourceLine(r searchResult) string {
	parts := make([]string, 0, 2)
	if s := collapse(r.SiteName); s != "" {
		parts = append(parts, s)
	}
	if s := collapse(r.PublishTime); s != "" {
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return ""
	}
	return "— " + strings.Join(parts, " · ")
}

// upstreamMessage 从上游正文里挑一句能给人看的：优先信封/网关的错误字段，
// 否则退回截断后的原文(AI.Core 出错时回的是纯文本)。
func upstreamMessage(payload []byte) string {
	var probe struct {
		Message string `json:"Message"`
		Error   any    `json:"error"`
	}
	if err := json.Unmarshal(payload, &probe); err == nil {
		if m := strings.TrimSpace(probe.Message); m != "" {
			return truncate(m)
		}
		switch e := probe.Error.(type) {
		case string:
			if m := strings.TrimSpace(e); m != "" {
				return truncate(m)
			}
		case map[string]any:
			if m, ok := e["message"].(string); ok && strings.TrimSpace(m) != "" {
				return truncate(strings.TrimSpace(m))
			}
		}
	}
	return truncate(fallback(collapse(string(payload)), "(无返回内容)"))
}

func errorResult(text string) tool.Result {
	return tool.Result{IsError: true, Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: text}}}
}

// emitOutput 发一行尽力而为的进度，永不阻塞搜索。
func emitOutput(events chan<- tool.Event, line string) {
	if events == nil {
		return
	}
	select {
	case events <- tool.Event{Type: tool.EventTypeOutput, Output: []tool.OutputLine{{Stream: tool.OutputStreamStdout, Text: line}}}:
	default:
	}
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func fallback(s, or string) string {
	if s == "" {
		return or
	}
	return s
}

// truncate 按 rune 截断，避免把一个中文字切成半个。
func truncate(s string) string {
	if len(s) <= maxErrorText {
		return s
	}
	r := []rune(s)
	for len(string(r)) > maxErrorText {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}
