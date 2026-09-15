package codexproxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/wt68/runcode/internal/codexauth"
)

// upstream stands in for the Codex backend and records what reached it.
type upstream struct {
	srv    *httptest.Server
	reqs   []*http.Request
	bodies []string
}

func newUpstream(t *testing.T, h http.HandlerFunc) *upstream {
	t.Helper()
	u := &upstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.reqs = append(u.reqs, r)
		u.bodies = append(u.bodies, string(body))
		h(w, r)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

// start brings up a proxy resolving one profile to up.
func start(t *testing.T, profile string, up Upstream) *Server {
	t.Helper()
	s, err := Start(func(p string) (Upstream, bool) {
		if p != profile {
			return Upstream{}, false
		}
		return up, true
	}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// The ChatGPT-subscription path: the proxy injects the bearer, the client
// fingerprint headers the upstream gates models on, and the account id — none of
// which the engine knows how to send.
func TestInjectsCredentialsAndCodexHeaders(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {}\n\n")
	})
	s := start(t, "chatgpt", Upstream{
		BaseURL:   up.srv.URL + "/backend-api/codex",
		Bearer:    func() (string, error) { return "AT", nil },
		AccountID: "acct-1",
	})

	resp := post(t, s.BaseURL("chatgpt")+"/responses", `{"model":"gpt-5-codex"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if len(up.reqs) != 1 {
		t.Fatalf("upstream saw %d requests, want 1", len(up.reqs))
	}
	got := up.reqs[0]
	if got.URL.Path != "/backend-api/codex/responses" {
		t.Fatalf("upstream path = %q, want the base URL with /responses appended", got.URL.Path)
	}
	for header, want := range map[string]string{
		"Authorization":      "Bearer AT",
		"originator":         codexauth.Originator,
		"version":            codexauth.ClientVersion,
		"chatgpt-account-id": "acct-1",
	} {
		if v := got.Header.Get(header); v != want {
			t.Errorf("%s = %q, want %q", header, v, want)
		}
	}
	// 请求体不是原样转发的：Codex 后端要求的改写在这里发生（见 codexBody）。
	var sent map[string]any
	if err := json.Unmarshal([]byte(up.bodies[0]), &sent); err != nil {
		t.Fatalf("forwarded body is not JSON: %v", err)
	}
	if sent["model"] != "gpt-5-codex" {
		t.Fatalf("model = %v, want it preserved", sent["model"])
	}
}

// ChatGPT 的 Codex 后端不接受标准 Responses 请求体的全部字段。引擎发的是通用
// 请求，差异必须在代理这里抹平，否则每次都是 400——max_output_tokens 尤其致命，
// 引擎总会发它。
func TestCodexBodyMeetsBackendContract(t *testing.T) {
	// 引擎实际会发的形状：带 max_output_tokens、不带 tools/parallel_tool_calls。
	raw := `{"model":"gpt-5-codex","input":[{"type":"message","role":"user"}],` +
		`"max_output_tokens":32000,"temperature":0.7,"top_p":0.9,"store":false,"stream":true}`

	var got map[string]any
	if err := json.Unmarshal(codexBody([]byte(raw), "sess-1"), &got); err != nil {
		t.Fatalf("transformed body is not JSON: %v", err)
	}

	for _, k := range []string{"max_output_tokens", "temperature", "top_p"} {
		if _, present := got[k]; present {
			t.Errorf("%s survived; the ChatGPT backend rejects it (this is the 400)", k)
		}
	}
	if got["instructions"] != "" {
		t.Errorf("instructions = %v, want the required field defaulted", got["instructions"])
	}
	if tools, ok := got["tools"].([]any); !ok || len(tools) != 0 {
		t.Errorf("tools = %v, want an empty list rather than a missing field", got["tools"])
	}
	if got["parallel_tool_calls"] != false {
		t.Errorf("parallel_tool_calls = %v, want false (responses-lite 的硬要求)", got["parallel_tool_calls"])
	}
	if got["store"] != false || got["stream"] != true {
		t.Errorf("store/stream = %v/%v, want false/true", got["store"], got["stream"])
	}
	inc, _ := got["include"].([]any)
	if len(inc) != 1 || inc[0] != reasoningInclude {
		t.Errorf("include = %v, want the encrypted reasoning marker", got["include"])
	}
	// 调用方自己的字段不能被动到。
	if got["model"] != "gpt-5-codex" || got["input"] == nil {
		t.Errorf("the caller's own fields were disturbed: %v", got)
	}
}

// 调用方已经给了的必填字段要保留，不能被默认值盖掉；include 里已有的项也要留着。
func TestCodexBodyKeepsCallerValues(t *testing.T) {
	raw := `{"instructions":"be brief","tools":[{"type":"function","name":"Read"}],` +
		`"parallel_tool_calls":true,"reasoning":{"effort":"high"},"include":["something.else"]}`
	var got map[string]any
	if err := json.Unmarshal(codexBody([]byte(raw), "sess-1"), &got); err != nil {
		t.Fatalf("transformed body is not JSON: %v", err)
	}
	if got["instructions"] != "be brief" {
		t.Errorf("instructions = %v, want the caller's", got["instructions"])
	}
	if tools, _ := got["tools"].([]any); len(tools) != 1 {
		t.Errorf("tools = %v, want the caller's list", got["tools"])
	}
	// 调用方就算显式要并行也不行：lite 通道不接受，上游会 400。
	if got["parallel_tool_calls"] != false {
		t.Errorf("parallel_tool_calls = %v, want false regardless of the caller", got["parallel_tool_calls"])
	}
	inc, _ := got["include"].([]any)
	if len(inc) != 2 || inc[0] != "something.else" || inc[1] != reasoningInclude {
		t.Errorf("include = %v, want the caller's entry kept and the marker appended", got["include"])
	}
}

// 不是 JSON 就原样放行：让上游去回绝，代理不该把"格式不对"变成"请求被吃了"。
func TestCodexBodyPassesThroughNonJSON(t *testing.T) {
	raw := []byte("not json at all")
	if got := codexBody(raw, "sess-1"); string(got) != string(raw) {
		t.Fatalf("codexBody(%q) = %q, want it untouched", raw, got)
	}
}

// A third-party Codex relay has no ChatGPT account: the header must be absent
// rather than sent empty, which some relays reject.
func TestThirdPartyRelayOmitsAccountHeader(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") })
	s := start(t, "relay", Upstream{
		BaseURL: up.srv.URL + "/v1",
		Bearer:  func() (string, error) { return "sk-relay", nil },
	})

	post(t, s.BaseURL("relay")+"/responses", "{}")

	got := up.reqs[0]
	if got.Header.Get("Authorization") != "Bearer sk-relay" {
		t.Fatalf("Authorization = %q, want the relay's API key", got.Header.Get("Authorization"))
	}
	if _, ok := got.Header["Chatgpt-Account-Id"]; ok {
		t.Fatal("chatgpt-account-id was sent to a relay that has no ChatGPT account")
	}
	if got.URL.Path != "/v1/responses" {
		t.Fatalf("upstream path = %q, want the relay's own base URL honored", got.URL.Path)
	}
}

// The loopback port is reachable by every process on the machine, so the path
// secret is the only thing standing between a local program and the user's
// ChatGPT quota.
func TestWrongSecretIsNotServed(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") })
	s := start(t, "p", Upstream{BaseURL: up.srv.URL, Bearer: func() (string, error) { return "AT", nil }})

	// Same port, same profile, guessed secret.
	forged := strings.Replace(s.BaseURL("p"), s.secret, strings.Repeat("0", len(s.secret)), 1)
	resp := post(t, forged+"/responses", "{}")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a wrong secret", resp.StatusCode)
	}
	if len(up.reqs) != 0 {
		t.Fatal("a request with the wrong secret reached the upstream")
	}
}

// Only the one path the engine actually calls is served; the proxy is not a
// general-purpose forwarder someone can point anywhere.
func TestOnlyResponsesPathIsServed(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") })
	s := start(t, "p", Upstream{BaseURL: up.srv.URL, Bearer: func() (string, error) { return "AT", nil }})

	for _, path := range []string{"/chat/completions", "/models", "/responses/compact"} {
		resp := post(t, s.BaseURL("p")+path, "{}")
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s gave %d, want 404", path, resp.StatusCode)
		}
	}
	if len(up.reqs) != 0 {
		t.Fatal("an unexpected path was forwarded upstream")
	}
}

// An unknown profile must not fall through to some other profile's credentials.
func TestUnknownProfileIsRejected(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") })
	s := start(t, "known", Upstream{BaseURL: up.srv.URL, Bearer: func() (string, error) { return "AT", nil }})

	resp := post(t, s.BaseURL("other")+"/responses", "{}")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unknown profile", resp.StatusCode)
	}
	if len(up.reqs) != 0 {
		t.Fatal("an unknown profile reached the upstream")
	}
}

// No usable token reads as 401 so the engine runs its existing refresh-and-retry
// path instead of surfacing an opaque failure.
func TestMissingCredentialIsUnauthorized(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") })
	s := start(t, "p", Upstream{
		BaseURL: up.srv.URL,
		Bearer:  func() (string, error) { return "", errors.New("未登录 ChatGPT") },
	})

	resp := post(t, s.BaseURL("p")+"/responses", "{}")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "未登录 ChatGPT") {
		t.Fatalf("body = %q, want the reason to reach the caller", body)
	}
	if len(up.reqs) != 0 {
		t.Fatal("a credential-less request reached the upstream")
	}
}

// A bearer that returns empty without an error is still unusable; sending
// "Bearer " upstream would produce a confusing 401 from the far side instead.
func TestEmptyTokenIsUnauthorized(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") })
	s := start(t, "p", Upstream{BaseURL: up.srv.URL, Bearer: func() (string, error) { return "  ", nil }})

	if resp := post(t, s.BaseURL("p")+"/responses", "{}"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an empty token", resp.StatusCode)
	}
	if len(up.reqs) != 0 {
		t.Fatal("an empty-token request reached the upstream")
	}
}

// Upstream failures keep their status and reason.
func TestUpstreamStatusAndBodyAreForwarded(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
	})
	s := start(t, "p", Upstream{BaseURL: up.srv.URL, Bearer: func() (string, error) { return "AT", nil }})

	resp := post(t, s.BaseURL("p")+"/responses", "{}")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want the upstream's 429 passed through", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "rate limited") {
		t.Fatalf("body = %q, want the upstream's reason", body)
	}
}

// ChatGPT 报错用 {"detail":...}，而引擎只认 {"error":{"message":...}}——认不出来就
// 退回状态行，用户只看到 "400 Bad Request"。实测过的真实例子：上游明说了模型不能
// 用于 ChatGPT 账号，界面上却什么都看不到。翻译必须发生在代理这一层。
func TestUpstreamDetailErrorIsTranslated(t *testing.T) {
	const detail = "The 'gpt-5-codex' model is not supported when using Codex with a ChatGPT account."
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"detail":`+strconv.Quote(detail)+`}`)
	})
	s := start(t, "p", Upstream{BaseURL: up.srv.URL, Bearer: func() (string, error) { return "AT", nil }})

	resp := post(t, s.BaseURL("p")+"/responses", "{}")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want the upstream's 400", resp.StatusCode)
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("response is not an error envelope: %v (%s)", err, body)
	}
	if env.Error.Message != detail {
		t.Fatalf("message = %q, want the upstream's reason verbatim", env.Error.Message)
	}
	if env.Error.Type == "" {
		t.Fatal("envelope has no type; the engine classifies on it")
	}
}

// 各种奇形怪状的错误正文都要留下线索，不能变成一句没内容的失败。
func TestCodexErrorShapes(t *testing.T) {
	// 已经是标准信封:原样放行,不重新包一层。
	std := []byte(`{"error":{"message":"already fine","type":"x"}}`)
	if got := codexError(std); string(got) != string(std) {
		t.Errorf("standard envelope was rewritten: %s", got)
	}
	// detail 是对象(校验类错误):信息不能丢。
	got := string(codexError([]byte(`{"detail":{"field":"model","reason":"unknown"}}`)))
	if !strings.Contains(got, "model") || !strings.Contains(got, "unknown") {
		t.Errorf("structured detail lost information: %s", got)
	}
	// 连 JSON 都不是(网关的 HTML 错误页):至少让人看见内容。
	got = string(codexError([]byte("<html>502 upstream down</html>")))
	if !strings.Contains(got, "502 upstream down") {
		t.Errorf("non-JSON body lost information: %s", got)
	}
	// 空正文:也要是一个合法信封,而不是空字符串。
	var env map[string]any
	if err := json.Unmarshal(codexError(nil), &env); err != nil || env["error"] == nil {
		t.Errorf("empty body did not produce an envelope: %v", err)
	}
}

// Profile names are user-chosen and may carry slashes or CJK; they must survive
// the round trip through the URL path.
func TestProfileNameRoundTrip(t *testing.T) {
	for _, name := range []string{"我的 Codex", "a/b", "plain"} {
		if got := decodeProfile(encodeProfile(name)); got != name {
			t.Errorf("round trip of %q gave %q", name, got)
		}
	}
	if decodeProfile("zzz") != "" {
		t.Error("a non-hex path segment must decode to empty, not garbage")
	}
}

// Close is idempotent: session teardown paths may call it more than once.
func TestCloseIsIdempotent(t *testing.T) {
	s := start(t, "p", Upstream{BaseURL: "http://example.invalid", Bearer: func() (string, error) { return "AT", nil }})
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// A nil resolver is a wiring bug; it must fail at Start rather than 404 forever.
func TestStartRequiresResolver(t *testing.T) {
	if _, err := Start(nil, nil); err == nil {
		t.Fatal("Start accepted a nil resolver")
	}
}

// ChatGPT is unreachable without a proxy on many networks, so forwarding must be
// able to go through one — and the proxy is resolved per request, so changing the
// setting takes effect immediately instead of at the next session.
func TestForwardingHonorsProxy(t *testing.T) {
	// The "proxy" is just an HTTP server that records what it was asked to fetch.
	var proxied []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied = append(proxied, r.URL.String())
		_, _ = io.WriteString(w, `{"via":"proxy"}`)
	}))
	t.Cleanup(proxy.Close)
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}

	// A per-request function, so the answer can change between requests.
	usingProxy := true
	s, err := Start(
		func(string) (Upstream, bool) {
			return Upstream{BaseURL: "http://upstream.invalid/v1", Bearer: func() (string, error) { return "AT", nil }}, true
		},
		func(*http.Request) (*url.URL, error) {
			if usingProxy {
				return proxyURL, nil
			}
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	resp := post(t, s.BaseURL("p")+"/responses", "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want the proxied response", resp.StatusCode)
	}
	if len(proxied) != 1 || !strings.Contains(proxied[0], "upstream.invalid") {
		t.Fatalf("proxy saw %v, want the upstream request routed through it", proxied)
	}

	// Turning the proxy off takes effect on the very next request: the upstream
	// host does not resolve, so a direct attempt must fail rather than silently
	// keep using the old proxy.
	usingProxy = false
	if resp := post(t, s.BaseURL("p")+"/responses", "{}"); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 once the proxy was removed", resp.StatusCode)
	}
	if len(proxied) != 1 {
		t.Fatalf("proxy saw %d requests, want it unused after being turned off", len(proxied))
	}
}

// 客户端要有个正经身份：不设 User-Agent 的话 Go 会发 "Go-http-client/1.1"，
// 对面既看不出这是谁，也有充分理由把它当可疑流量。
func TestUserAgentIdentifiesThisClient(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") })
	s := start(t, "p", Upstream{
		BaseURL:   up.srv.URL,
		Bearer:    func() (string, error) { return "AT", nil },
		UserAgent: "zhikai/1.0.1",
	})

	post(t, s.BaseURL("p")+"/responses", "{}")
	if got := up.reqs[0].Header.Get("User-Agent"); got != "zhikai/1.0.1" {
		t.Fatalf("User-Agent = %q, want the host's own identity", got)
	}
}

// 宿主没给标识时也不能退回 Go 的默认值。
func TestUserAgentFallsBackToOurOwnName(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") })
	s := start(t, "p", Upstream{BaseURL: up.srv.URL, Bearer: func() (string, error) { return "AT", nil }})

	post(t, s.BaseURL("p")+"/responses", "{}")
	got := up.reqs[0].Header.Get("User-Agent")
	if got != defaultUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, defaultUserAgent)
	}
	if strings.Contains(got, "Go-http-client") {
		t.Fatal("退回了 Go 的默认 User-Agent")
	}
}

// responses-lite 这个头与请求体的两条要求必须同进同退：只加头不改体，上游每次都
// 400（实测原话："requires `parallel_tool_calls` to be false" /
// "requires `reasoning.context` to be `all_turns`"）。
func TestLiteHeaderAndItsBodyRequirementsGoTogether(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") })
	s := start(t, "p", Upstream{BaseURL: up.srv.URL, Bearer: func() (string, error) { return "AT", nil }})

	// 调用方送的是"并行开、没有 reasoning.context"——正是会被上游回绝的那种。
	post(t, s.BaseURL("p")+"/responses", `{"model":"m","parallel_tool_calls":true}`)

	if got := up.reqs[0].Header.Get(liteHeader); got != "true" {
		t.Fatalf("%s = %q, want it sent", liteHeader, got)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(up.bodies[0]), &sent); err != nil {
		t.Fatalf("forwarded body is not JSON: %v", err)
	}
	if sent["parallel_tool_calls"] != false {
		t.Errorf("parallel_tool_calls = %v, want false alongside the lite header", sent["parallel_tool_calls"])
	}
	reasoning, _ := sent["reasoning"].(map[string]any)
	if reasoning["context"] != "all_turns" {
		t.Errorf("reasoning.context = %v, want all_turns alongside the lite header", reasoning["context"])
	}
}

// 钉住 reasoning.context 不能顺手把调用方设的 effort/summary 抹掉——那两个是模型
// 行为，与 lite 的要求无关。
func TestAllTurnsContextKeepsOtherReasoningFields(t *testing.T) {
	got := withAllTurnsContext(map[string]any{"effort": "high", "summary": "auto"})
	if got["context"] != "all_turns" {
		t.Errorf("context = %v, want all_turns", got["context"])
	}
	if got["effort"] != "high" || got["summary"] != "auto" {
		t.Errorf("reasoning = %v, want the caller's effort/summary preserved", got)
	}
	// 调用方完全没给 reasoning 时也要造出合法的一份。
	if withAllTurnsContext(nil)["context"] != "all_turns" {
		t.Error("a missing reasoning object did not get the required context")
	}
}

// 会话/回合标识：id 必须是**我们自己生成的**，且回合 id 每次请求都换——抄别人那份
// 既不诚实，服务端那边也对不上。
func TestSessionAndTurnHeaders(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") })
	s := start(t, "p", Upstream{BaseURL: up.srv.URL, Bearer: func() (string, error) { return "AT", nil }})

	post(t, s.BaseURL("p")+"/responses", "{}")
	post(t, s.BaseURL("p")+"/responses", "{}")

	first, second := up.reqs[0], up.reqs[1]

	// 会话标识随代理进程，两次请求必须相同——它是提示词缓存的关联依据。
	if got := first.Header.Get("session_id"); got == "" || got != second.Header.Get("session_id") {
		t.Fatalf("session_id 两次不一致或为空: %q / %q", got, second.Header.Get("session_id"))
	}
	if got := first.Header.Get("x-codex-window-id"); got != first.Header.Get("session_id")+":0" {
		t.Fatalf("x-codex-window-id = %q, want <session>:0", got)
	}
	if got := first.Header.Get("x-codex-beta-features"); got != betaFeatures {
		t.Fatalf("x-codex-beta-features = %q, want %q", got, betaFeatures)
	}

	// 回合标识每次请求都要换。
	turnOf := func(r *http.Request) string {
		var meta map[string]any
		if err := json.Unmarshal([]byte(r.Header.Get("x-codex-turn-metadata")), &meta); err != nil {
			t.Fatalf("turn metadata 不是 JSON: %v", err)
		}
		if meta["session_id"] != r.Header.Get("session_id") {
			t.Errorf("metadata 里的 session_id 与请求头不一致: %v", meta["session_id"])
		}
		if meta["turn_started_at_unix_ms"] == nil {
			t.Error("metadata 缺少 turn_started_at_unix_ms")
		}
		// 描述功能的字段一律不报：我们没有沙箱、没有自动复审，编一个值就是谎报。
		for _, k := range []string{"sandbox", "sandbox_mode", "auto_review_enabled", "node_repl_disabled", "agent_name"} {
			if _, present := meta[k]; present {
				t.Errorf("metadata 报了我们没有的功能字段 %q", k)
			}
		}
		id, _ := meta["turn_id"].(string)
		return id
	}
	a, b := turnOf(first), turnOf(second)
	if a == "" || a == b {
		t.Fatalf("turn_id 没有逐请求变化: %q / %q", a, b)
	}
}

// UUID 得是合法形状且每次不同——它是关联用的，重复会让上游把两条会话混在一起。
func TestNewUUIDShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		u := newUUID()
		if len(u) != 36 || strings.Count(u, "-") != 4 {
			t.Fatalf("newUUID() = %q, want 36 位带 4 个连字符", u)
		}
		if seen[u] {
			t.Fatalf("newUUID() 重复了: %q", u)
		}
		seen[u] = true
	}
}

// prompt_cache_key 决定上游能不能复用同一条会话的前缀缓存。它必须与 session_id
// 请求头是同一个值——两者代表同一条会话，对不上等于没开缓存。
func TestPromptCacheKeyMatchesSessionHeader(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") })
	s := start(t, "p", Upstream{BaseURL: up.srv.URL, Bearer: func() (string, error) { return "AT", nil }})

	post(t, s.BaseURL("p")+"/responses", `{"model":"m"}`)

	var sent map[string]any
	if err := json.Unmarshal([]byte(up.bodies[0]), &sent); err != nil {
		t.Fatalf("forwarded body is not JSON: %v", err)
	}
	key, _ := sent["prompt_cache_key"].(string)
	if key == "" {
		t.Fatal("请求体没有 prompt_cache_key，每个回合都会是冷启动")
	}
	if header := up.reqs[0].Header.Get("session_id"); key != header {
		t.Fatalf("prompt_cache_key=%q 与 session_id=%q 不一致，缓存关联不上", key, header)
	}
}

// 调用方自己带了就不覆盖：它比我们更清楚这条会话的边界。
func TestPromptCacheKeyKeepsCallerValue(t *testing.T) {
	var got map[string]any
	if err := json.Unmarshal(codexBody([]byte(`{"prompt_cache_key":"mine"}`), "sess-1"), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got["prompt_cache_key"] != "mine" {
		t.Fatalf("prompt_cache_key = %v, want the caller's", got["prompt_cache_key"])
	}
}
