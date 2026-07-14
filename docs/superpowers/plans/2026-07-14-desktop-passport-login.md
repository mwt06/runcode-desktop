# 桌面端通行证登录（子项目 C）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** runcode 桌面端接入 OUCOnline 通行证：系统浏览器 OIDC 登录（authorization_code + PKCE + Bridge 回调中转）、令牌自动续期（TokenSource）、平台模型列表（经 ouconline-ai-bridge）与自定义模型并存。

**Architecture:** 核心模块加一条 `TokenSource func() (string, error)` 贯通链（`engine.Config → llm.Config → openai.Options → httpClient` 每请求取令牌）；桌面 `internal/desktop` 新增 OAuth 机制（PKCE/回环回调/令牌交换）、令牌管理器（内存 + DPAPI 落盘 + 静默续期）与 Passport 绑定方法；前端 StartForm 改为登录优先，模型下拉合并平台模型与自定义模型。

**Tech Stack:** Go 1.x（现有工具链）、Wails v2、React/TS（现有前端）、标准库 `net/http`+`crypto`（无新依赖）。

**Spec:** `docs/superpowers/specs/2026-07-14-passport-bridge-integration-design.md`

## Global Constraints

- 工作分支：当前 `feature/session-streaming-history-transcript`（仓库有大量无关改动，**每次 commit 只 add 本任务列出的文件**）。
- 环境实测值（2026-07 核实，作为代码默认值）：
  - Passport authority：`https://passport-ai.ouchn.edu.cn`（authorize/token 端点在此域名下）
  - 令牌 iss 是 `https://passport-ai-dev.ouc-online.com.cn`——桌面端不校验 iss（只透传给 Bridge），无需处理
  - ClientId：`runcode-desktop`；scopes：`openid profile offline_access passportapi`；PKCE 仅 S256
  - Bridge 默认 `http://localhost:8199`；redirect_uri 恒为 `<bridge>/oauth/callback`；state 格式 `<nonce>.<本地端口>`（Bridge 按此中转回跳）
- 环境变量覆盖：`RUNCODE_PASSPORT_AUTHORITY`、`RUNCODE_PASSPORT_CLIENT_ID`、`RUNCODE_BRIDGE_BASE_URL`。
- 调 Passport/Bridge 用普通 `http.Client`（**禁止** `internal/webclient`——它拒连回环/内网）。
- 令牌只在 Go 侧持有；前端只拿登录态与用户信息。落盘走 DPAPI（`protectSecret`）；非 Windows `protectSecret` 返回 `ok=false` → 只存内存（本期约束）。
- 测试命令：核心 `go test ./pkg/llm/... ./internal/engine/... ./internal/desktop/...`；全量回归 `go test -race ./...`；桌面编译检查 `go -C cmd/runcode-desktop build ./...`；前端 `cd cmd/runcode-desktop/frontend && npm run build`。
- 提交信息风格沿用仓库现状（如 `desktop: ...` / `Desktop UI: ...`），结尾加 `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`。

## File Structure（最终形态）

```
pkg/llm/registry.go                    (改) Config 增加 TokenSource
pkg/llm/providers/openai/provider.go   (改) Options 增加 TokenSource；工厂透传
pkg/llm/providers/openai/client.go     (改) httpClient 每请求经 TokenSource 取 bearer
pkg/llm/providers/openai/client_token_test.go (新) TokenSource 行为测试
internal/engine/build.go               (改) BuildProvider 透传 TokenSource
internal/engine/config.go 或所在文件    (改) engine.Config 增加 TokenSource 字段
internal/desktop/oauth.go              (新) PKCE/state/authorize URL/令牌交换/回环回调服务器
internal/desktop/oauth_test.go         (新)
internal/desktop/tokens.go             (新) tokenManager + DPAPI 落盘
internal/desktop/tokens_test.go        (新)
internal/desktop/passport.go           (新) Passport 配置 + App 绑定方法 + 事件
internal/desktop/passport_test.go      (新)
internal/desktop/custommodels.go       (新) 自定义模型持久化 + App 方法
internal/desktop/custommodels_test.go  (新)
internal/desktop/types.go              (改) 事件常量/StartSessionRequest.CustomModels
internal/desktop/app.go                (改) App 字段 + StartSession passport 接线
cmd/runcode-desktop/frontend/src/bridge.ts    (改) 类型 + 包装函数 + 事件
cmd/runcode-desktop/frontend/src/wails.d.ts   (改) 方法声明
cmd/runcode-desktop/frontend/src/pages.tsx    (改) StartForm 登录区/模型下拉/自定义模型
```

---

### Task 1: 核心 TokenSource 贯通（pkg/llm + engine）

**Files:**
- Modify: `pkg/llm/registry.go:12-20`（Config 加字段）
- Modify: `pkg/llm/providers/openai/provider.go:24-63`（Options + 工厂）
- Modify: `pkg/llm/providers/openai/client.go:59-143`（httpClient）
- Modify: `internal/engine/build.go:253-263`（BuildProvider 透传）+ `engine.Config` 定义处（`grep -n "type Config struct" internal/engine` 定位）
- Test: `pkg/llm/providers/openai/client_token_test.go`

**Interfaces:**
- Consumes: 现有 `llm.Config`/`openai.Options`/`httpClient`
- Produces: `llm.Config.TokenSource func() (string, error)`、`openai.Options.TokenSource`、`engine.Config.TokenSource`——每次请求优先经 TokenSource 取 bearer，nil 时回落静态 `APIKey`/`AuthToken`。Task 5 消费 `engine.Config.TokenSource`。

- [ ] **Step 1: 写失败测试**

`pkg/llm/providers/openai/client_token_test.go`：

```go
package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// sseOK 返回一个最小可解析的 SSE 响应体。
func sseOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
}

func TestTokenSourceUsedPerRequest(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		sseOK(w)
	}))
	defer srv.Close()

	calls := 0
	c := newHTTPClient(Options{
		BaseURL: srv.URL,
		APIKey:  "static-key", // TokenSource 应优先于静态 key
		TokenSource: func() (string, error) {
			calls++
			if calls == 1 {
				return "tok-1", nil
			}
			return "tok-2", nil
		},
	})

	for i := 0; i < 2; i++ {
		sse, err := c.stream(context.Background(), chatRequest{})
		if err != nil {
			t.Fatalf("stream %d: %v", i, err)
		}
		_ = sse.Close()
	}
	if len(got) != 2 || got[0] != "Bearer tok-1" || got[1] != "Bearer tok-2" {
		t.Fatalf("authorization headers = %v, want per-request tokens", got)
	}
}

func TestTokenSourceErrorFailsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request must not reach the server when the token source fails")
	}))
	defer srv.Close()

	c := newHTTPClient(Options{
		BaseURL:     srv.URL,
		TokenSource: func() (string, error) { return "", errors.New("请重新登录") },
	})
	if _, err := c.stream(context.Background(), chatRequest{}); err == nil {
		t.Fatal("want error from failing token source")
	}
}

func TestNilTokenSourceFallsBackToStaticKey(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		sseOK(w)
	}))
	defer srv.Close()

	c := newHTTPClient(Options{BaseURL: srv.URL, APIKey: "static-key"})
	sse, err := c.stream(context.Background(), chatRequest{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	_ = sse.Close()
	if got != "Bearer static-key" {
		t.Fatalf("authorization = %q, want static key fallback", got)
	}
}
```

（若 `sse.Close()`/`chatRequest{}` 与现有 `client_test.go` 的构造惯例不一致，以现有测试文件的写法为准调整——断言不变。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/llm/providers/openai/ -run TokenSource -v`
Expected: 编译错误 `unknown field TokenSource in struct literal`。

- [ ] **Step 3: 实现**

`pkg/llm/registry.go` 的 `Config`（第 12-20 行）加字段：

```go
type Config struct {
	APIKey           string
	AuthToken        string
	BaseURL          string
	DefaultMaxTokens int
	MaxContextTokens int
	MaxRetries       int
	Options          map[string]string
	// TokenSource, when set, is consulted per request for a fresh bearer token
	// (e.g. an OAuth access token that auto-refreshes). It takes precedence over
	// APIKey/AuthToken; a nil TokenSource keeps the static-credential behavior.
	TokenSource func() (string, error)
}
```

`pkg/llm/providers/openai/provider.go`：`Options` 加同名字段（注释同上），`init()` 工厂加 `TokenSource: cfg.TokenSource,`。

`pkg/llm/providers/openai/client.go`：
1. `httpClient` 结构体（59-66 行）加 `tokenSource func() (string, error)`；
2. `newHTTPClient`（68-89 行）返回值加 `tokenSource: opts.TokenSource,`；
3. `attempt`（134-143 行）把静态 bearer 换成按请求解析：

```go
	bearer := c.bearer
	if c.tokenSource != nil {
		tok, err := c.tokenSource()
		if err != nil {
			// 凭证取不到（如刷新令牌已过期）不是可重试的瞬时故障
			return nil, 0, false, fmt.Errorf("openai: token source: %w", err)
		}
		bearer = tok
	}
	if bearer != "" {
		httpReq.Header.Set("Authorization", "Bearer "+bearer)
	}
```

`internal/engine`：`Config` 定义处加字段（注释：`// TokenSource supplies a per-request bearer token (OAuth). Overrides APIKey/AuthToken when set.`）：

```go
	TokenSource func() (string, error)
```

`internal/engine/build.go` `BuildProvider`（253 行起）的 `llm.Config{...}` 加 `TokenSource: cfg.TokenSource,`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/llm/providers/openai/ -run TokenSource -v` → PASS（3 个用例）。
Run: `go test ./pkg/llm/... ./internal/engine/...` → 全绿（确认无回归）。

- [ ] **Step 5: Commit**

```powershell
git add pkg/llm/registry.go pkg/llm/providers/openai/provider.go pkg/llm/providers/openai/client.go pkg/llm/providers/openai/client_token_test.go internal/engine/build.go <engine.Config所在文件>
git commit -m "llm/openai: per-request TokenSource bearer (OAuth 自动续期基础)"
```

---

### Task 2: OAuth 机制（PKCE / state / authorize URL / 令牌交换）

**Files:**
- Create: `internal/desktop/oauth.go`
- Test: `internal/desktop/oauth_test.go`

**Interfaces:**
- Consumes: 标准库
- Produces（Task 3/4/5 消费）:
  - `type tokenSet struct { AccessToken, RefreshToken string; Expiry time.Time }`
  - `genPKCE() (verifier, challenge string, err error)`
  - `genState(port int) (state string, err error)`（`<nonce>.<port>`，nonce 为 base64url，天然不含 `.`）
  - `stateNonce(state string) string`（取最后一个 `.` 前的部分）
  - `buildAuthorizeURL(authority, clientID, redirectURI, scopes, state, challenge string) string`
  - `exchangeCode(ctx context.Context, hc *http.Client, tokenURL, clientID, code, verifier, redirectURI string) (tokenSet, error)`
  - `refreshGrant(ctx context.Context, hc *http.Client, tokenURL, clientID, refreshToken string) (tokenSet, error)`

- [ ] **Step 1: 写失败测试**

`internal/desktop/oauth_test.go`：

```go
package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGenPKCE(t *testing.T) {
	verifier, challenge, err := genPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		t.Fatalf("verifier length %d out of RFC 7636 range", len(verifier))
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if challenge != want {
		t.Fatalf("challenge = %q, want S256(verifier) = %q", challenge, want)
	}
	v2, _, _ := genPKCE()
	if v2 == verifier {
		t.Fatal("two PKCE verifiers must differ")
	}
}

func TestGenStateCarriesPort(t *testing.T) {
	state, err := genState(53699)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(state, ".53699") {
		t.Fatalf("state %q must end with .<port>", state)
	}
	if nonce := stateNonce(state); nonce == "" || strings.Contains(nonce, ".") {
		t.Fatalf("nonce %q malformed", nonce)
	}
}

func TestBuildAuthorizeURL(t *testing.T) {
	u := buildAuthorizeURL("https://passport.example", "runcode-desktop",
		"http://localhost:8199/oauth/callback", "openid profile offline_access passportapi",
		"n.51000", "CHALLENGE")
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/connect/authorize" {
		t.Fatalf("path = %q", parsed.Path)
	}
	q := parsed.Query()
	for k, want := range map[string]string{
		"client_id":             "runcode-desktop",
		"redirect_uri":          "http://localhost:8199/oauth/callback",
		"response_type":         "code",
		"scope":                 "openid profile offline_access passportapi",
		"state":                 "n.51000",
		"code_challenge":        "CHALLENGE",
		"code_challenge_method": "S256",
	} {
		if q.Get(k) != want {
			t.Fatalf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestExchangeCode(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT","refresh_token":"RT","expires_in":3600,"token_type":"Bearer"}`))
	}))
	defer srv.Close()

	ts, err := exchangeCode(context.Background(), srv.Client(), srv.URL, "runcode-desktop",
		"CODE", "VERIFIER", "http://localhost:8199/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	if ts.AccessToken != "AT" || ts.RefreshToken != "RT" {
		t.Fatalf("tokenSet = %+v", ts)
	}
	if until := time.Until(ts.Expiry); until < 55*time.Minute || until > 61*time.Minute {
		t.Fatalf("expiry %v not ≈ now+1h", ts.Expiry)
	}
	for k, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "CODE",
		"code_verifier": "VERIFIER",
		"client_id":     "runcode-desktop",
		"redirect_uri":  "http://localhost:8199/oauth/callback",
	} {
		if form.Get(k) != want {
			t.Fatalf("form %s = %q, want %q", k, form.Get(k), want)
		}
	}
}

func TestExchangeCodeErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	if _, err := exchangeCode(context.Background(), srv.Client(), srv.URL, "c", "x", "v", "r"); err == nil ||
		!strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("err = %v, want invalid_grant surfaced", err)
	}
}

func TestRefreshGrant(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT2","refresh_token":"RT2","expires_in":3600}`))
	}))
	defer srv.Close()

	ts, err := refreshGrant(context.Background(), srv.Client(), srv.URL, "runcode-desktop", "RT1")
	if err != nil {
		t.Fatal(err)
	}
	if ts.AccessToken != "AT2" || ts.RefreshToken != "RT2" {
		t.Fatalf("tokenSet = %+v", ts)
	}
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "RT1" {
		t.Fatalf("form = %v", form)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop/ -run 'PKCE|State|AuthorizeURL|Exchange|Refresh' -v`
Expected: 编译错误（`genPKCE` 等未定义）。

- [ ] **Step 3: 实现 `internal/desktop/oauth.go`**

```go
package desktop

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// OAuth (authorization_code + PKCE) 机制层：纯函数 + 明确输入输出，
// 与 App/窗口系统解耦，便于单测。登录编排在 passport.go。

// tokenSet 是令牌端点响应的解析结果。Expiry 为绝对时间（now + expires_in）。
type tokenSet struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
}

// genPKCE 生成 RFC 7636 的 code_verifier 与 S256 code_challenge。
func genPKCE() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf) // 43 chars
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// genState 生成 <nonce>.<port> 形式的 state：Bridge 中转端点按端口回跳，
// 桌面端回调时校验 nonce。base64url 字母表不含 '.'，分隔无歧义。
func genState(port int) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf) + "." + strconv.Itoa(port), nil
}

// stateNonce 取 state 中最后一个 '.' 之前的随机部分。
func stateNonce(state string) string {
	if i := strings.LastIndex(state, "."); i > 0 {
		return state[:i]
	}
	return ""
}

// buildAuthorizeURL 组装 Passport 授权端点 URL。
func buildAuthorizeURL(authority, clientID, redirectURI, scopes, state, challenge string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", scopes)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	return strings.TrimRight(authority, "/") + "/connect/authorize?" + q.Encode()
}

// exchangeCode 用授权码 + PKCE verifier 到令牌端点换取令牌。
func exchangeCode(ctx context.Context, hc *http.Client, tokenURL, clientID, code, verifier, redirectURI string) (tokenSet, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("client_id", clientID)
	form.Set("redirect_uri", redirectURI)
	return postTokenForm(ctx, hc, tokenURL, form)
}

// refreshGrant 用 refresh token 静默续期。
func refreshGrant(ctx context.Context, hc *http.Client, tokenURL, clientID, refreshToken string) (tokenSet, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	return postTokenForm(ctx, hc, tokenURL, form)
}

func postTokenForm(ctx context.Context, hc *http.Client, tokenURL string, form url.Values) (tokenSet, error) {
	tokenURL = strings.TrimRight(tokenURL, "/")
	if !strings.HasSuffix(tokenURL, "/connect/token") && !strings.Contains(tokenURL, "://127.0.0.1") && !strings.Contains(tokenURL, "://localhost") {
		// authority 传进来时补上端点路径；测试传 httptest URL 原样用
		tokenURL += "/connect/token"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenSet{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return tokenSet{}, fmt.Errorf("请求令牌端点失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return tokenSet{}, fmt.Errorf("令牌端点返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return tokenSet{}, fmt.Errorf("解析令牌响应失败: %w", err)
	}
	if payload.AccessToken == "" {
		return tokenSet{}, fmt.Errorf("令牌响应缺少 access_token")
	}
	return tokenSet{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second),
	}, nil
}
```

**注意**：`postTokenForm` 里"补 /connect/token 路径"的启发式是为了让调用方既能传 authority 也能传完整测试 URL——如觉脆弱可改为：调用方一律传完整 token URL，测试传 `srv.URL`，生产传 `authority + "/connect/token"`（推荐后者，实现时把启发式删掉、由 passport.go 拼好完整 URL 传入，测试已按"完整 URL 原样用"编写，两种写法都能过）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop/ -run 'PKCE|State|AuthorizeURL|Exchange|Refresh' -v` → PASS（6 用例）。

- [ ] **Step 5: Commit**

```powershell
git add internal/desktop/oauth.go internal/desktop/oauth_test.go
git commit -m "desktop: OAuth PKCE/state/令牌交换机制层"
```

---

### Task 3: 回环回调服务器

**Files:**
- Modify: `internal/desktop/oauth.go`（追加）
- Test: `internal/desktop/oauth_test.go`（追加）

**Interfaces:**
- Produces: `startCallbackServer() (*callbackServer, error)`；`type callbackServer struct { Port int; Result <-chan callbackResult }`，`(*callbackServer).ExpectState(state string)`、`(*callbackServer).Close()`；`type callbackResult struct { Code string; Err error }`

- [ ] **Step 1: 追加失败测试到 `oauth_test.go`**

```go
func TestCallbackServerDeliversCode(t *testing.T) {
	cs, err := startCallbackServer()
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	state, _ := genState(cs.Port)
	cs.ExpectState(state)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=THE-CODE&state=%s", cs.Port, url.QueryEscape(state)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	select {
	case r := <-cs.Result:
		if r.Err != nil || r.Code != "THE-CODE" {
			t.Fatalf("result = %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no result delivered")
	}
}

func TestCallbackServerRejectsWrongState(t *testing.T) {
	cs, err := startCallbackServer()
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	state, _ := genState(cs.Port)
	cs.ExpectState(state)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=X&state=forged.%d", cs.Port, cs.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	select {
	case r := <-cs.Result:
		t.Fatalf("must not deliver on forged state, got %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestCallbackServerDeliversProviderError(t *testing.T) {
	cs, err := startCallbackServer()
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	state, _ := genState(cs.Port)
	cs.ExpectState(state)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?error=access_denied&state=%s", cs.Port, url.QueryEscape(state)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case r := <-cs.Result:
		if r.Err == nil || !strings.Contains(r.Err.Error(), "access_denied") {
			t.Fatalf("result = %+v, want access_denied error", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no result delivered")
	}
}
```

（测试文件需补 import `fmt`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop/ -run CallbackServer -v`
Expected: 编译错误（`startCallbackServer` 未定义）。

- [ ] **Step 3: 实现（追加到 `oauth.go`）**

```go
// callbackResult 是一次授权回调的结果：code 或错误，二选一。
type callbackResult struct {
	Code string
	Err  error
}

// callbackServer 是登录期间临时监听 127.0.0.1 随机端口的一次性回调接收器。
// Bridge 的 /oauth/callback 会按 state 里的端口把浏览器 302 回跳到这里。
type callbackServer struct {
	Port   int
	Result chan callbackResult

	mu       sync.Mutex
	expected string
	done     bool
	srv      *http.Server
	ln       net.Listener
}

// startCallbackServer 绑定 127.0.0.1 的随机端口并开始服务 /callback。
// 调用方拿到端口后生成 state，再 ExpectState 告知校验值。
func startCallbackServer() (*callbackServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("绑定回环端口失败: %w", err)
	}
	cs := &callbackServer{
		Port:   ln.Addr().(*net.TCPAddr).Port,
		Result: make(chan callbackResult, 1),
		ln:     ln,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", cs.handle)
	cs.srv = &http.Server{Handler: mux}
	go func() { _ = cs.srv.Serve(ln) }()
	return cs, nil
}

// ExpectState 设定本次登录合法的 state 值（含 nonce），此前到达的请求一律 400。
func (cs *callbackServer) ExpectState(state string) {
	cs.mu.Lock()
	cs.expected = state
	cs.mu.Unlock()
}

func (cs *callbackServer) handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cs.mu.Lock()
	expected, done := cs.expected, cs.done
	cs.mu.Unlock()
	if done || expected == "" || q.Get("state") != expected {
		http.Error(w, "state 校验失败", http.StatusBadRequest)
		return
	}

	var result callbackResult
	if e := q.Get("error"); e != "" {
		desc := q.Get("error_description")
		result.Err = fmt.Errorf("授权失败: %s %s", e, desc)
	} else if code := q.Get("code"); code != "" {
		result.Code = code
	} else {
		result.Err = fmt.Errorf("回调缺少 code")
	}

	cs.mu.Lock()
	cs.done = true
	cs.mu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if result.Err != nil {
		_, _ = w.Write([]byte("<html><body style='font-family:sans-serif'><h3>登录未完成</h3><p>请返回应用重试。</p></body></html>"))
	} else {
		_, _ = w.Write([]byte("<html><body style='font-family:sans-serif'><h3>登录成功</h3><p>您可以关闭此页面并返回应用。</p></body></html>"))
	}
	cs.Result <- result
}

// Close 停止监听。可安全多次调用。
func (cs *callbackServer) Close() {
	_ = cs.srv.Close()
}
```

（`oauth.go` 需补 import `net`、`sync`。）

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop/ -run CallbackServer -v` → PASS（3 用例）。

- [ ] **Step 5: Commit**

```powershell
git add internal/desktop/oauth.go internal/desktop/oauth_test.go
git commit -m "desktop: 一次性回环回调服务器（state 校验 + 结果通道）"
```

---

### Task 4: tokenManager（内存 + DPAPI 落盘 + 静默续期）

**Files:**
- Create: `internal/desktop/tokens.go`
- Test: `internal/desktop/tokens_test.go`

**Interfaces:**
- Consumes: `tokenSet`、`refreshGrant`（Task 2）、`protectSecret/unprotectSecret`（现有）
- Produces（Task 5 消费）:
  - `newTokenManager(tokenURL, clientID string, hc *http.Client, onLoggedOut func()) *tokenManager`
  - `(*tokenManager).Set(ts tokenSet)`（含落盘）、`Clear()`（含删盘）、`LoggedIn() bool`
  - `(*tokenManager).Token() (string, error)`——满足 Task 1 的 `TokenSource` 签名；过期前 60s 内自动续期
  - `(*tokenManager).loadPersisted()`（启动时恢复）

- [ ] **Step 1: 写失败测试**

`internal/desktop/tokens_test.go`：

```go
package desktop

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenReturnsValidAccessToken(t *testing.T) {
	tm := newTokenManager("http://unused", "c", http.DefaultClient, nil)
	tm.setInMemory(tokenSet{AccessToken: "AT", RefreshToken: "RT", Expiry: time.Now().Add(time.Hour)})
	tok, err := tm.Token()
	if err != nil || tok != "AT" {
		t.Fatalf("tok=%q err=%v", tok, err)
	}
}

func TestTokenRefreshesNearExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT2","refresh_token":"RT2","expires_in":3600}`))
	}))
	defer srv.Close()

	tm := newTokenManager(srv.URL, "c", srv.Client(), nil)
	tm.setInMemory(tokenSet{AccessToken: "AT1", RefreshToken: "RT1", Expiry: time.Now().Add(10 * time.Second)}) // <60s
	tok, err := tm.Token()
	if err != nil || tok != "AT2" {
		t.Fatalf("tok=%q err=%v, want refreshed AT2", tok, err)
	}
	if !tm.LoggedIn() {
		t.Fatal("still logged in after refresh")
	}
}

func TestTokenRefreshFailureLogsOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	loggedOut := false
	tm := newTokenManager(srv.URL, "c", srv.Client(), func() { loggedOut = true })
	tm.setInMemory(tokenSet{AccessToken: "AT1", RefreshToken: "expired", Expiry: time.Now().Add(-time.Minute)})
	if _, err := tm.Token(); err == nil {
		t.Fatal("want error when refresh fails")
	}
	if !loggedOut || tm.LoggedIn() {
		t.Fatalf("loggedOut=%v LoggedIn=%v, want logout on refresh failure", loggedOut, tm.LoggedIn())
	}
}

func TestTokenWithoutLoginErrors(t *testing.T) {
	tm := newTokenManager("http://unused", "c", http.DefaultClient, nil)
	if _, err := tm.Token(); err == nil {
		t.Fatal("want error when not logged in")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop/ -run 'TestToken' -v` → 编译错误（`newTokenManager` 未定义）。

- [ ] **Step 3: 实现 `internal/desktop/tokens.go`**

```go
package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// tokenManager 持有 Passport 令牌：内存为准，Windows 上经 DPAPI 落盘以便重启恢复。
// Token() 同时就是 LLM 引擎的 TokenSource——每次请求前保证 access token 新鲜。
type tokenManager struct {
	tokenURL string // 完整令牌端点 URL
	clientID string
	hc       *http.Client
	// onLoggedOut 在静默续期失败（refresh token 失效）时回调，用于向前端广播登出。
	onLoggedOut func()

	mu sync.Mutex
	ts tokenSet
}

var errNotLoggedIn = errors.New("未登录通行证，请先登录")

// refreshSkew 提前续期窗口：access token 剩余寿命低于它就先刷新，避免边缘过期。
const refreshSkew = 60 * time.Second

func newTokenManager(tokenURL, clientID string, hc *http.Client, onLoggedOut func()) *tokenManager {
	return &tokenManager{tokenURL: tokenURL, clientID: clientID, hc: hc, onLoggedOut: onLoggedOut}
}

// Token 返回可用的 access token（TokenSource 签名）。临期/过期先静默续期；
// 续期失败视为登出（清空 + 通知），错误提示引导重新登录。
func (m *tokenManager) Token() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ts.AccessToken == "" && m.ts.RefreshToken == "" {
		return "", errNotLoggedIn
	}
	if time.Until(m.ts.Expiry) > refreshSkew {
		return m.ts.AccessToken, nil
	}
	if m.ts.RefreshToken == "" {
		m.clearLocked()
		return "", errNotLoggedIn
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fresh, err := refreshGrant(ctx, m.hc, m.tokenURL, m.clientID, m.ts.RefreshToken)
	if err != nil {
		m.clearLocked()
		if m.onLoggedOut != nil {
			go m.onLoggedOut()
		}
		return "", errors.Join(errNotLoggedIn, err)
	}
	m.ts = fresh
	persistTokens(fresh)
	return m.ts.AccessToken, nil
}

// Set 记录一次登录成功的令牌并落盘。
func (m *tokenManager) Set(ts tokenSet) {
	m.mu.Lock()
	m.ts = ts
	m.mu.Unlock()
	persistTokens(ts)
}

// setInMemory 仅设内存（测试用，不触盘）。
func (m *tokenManager) setInMemory(ts tokenSet) {
	m.mu.Lock()
	m.ts = ts
	m.mu.Unlock()
}

// Clear 登出：清内存与磁盘。
func (m *tokenManager) Clear() {
	m.mu.Lock()
	m.clearLocked()
	m.mu.Unlock()
}

func (m *tokenManager) clearLocked() {
	m.ts = tokenSet{}
	if path, err := passportTokenPath(); err == nil {
		_ = os.Remove(path)
	}
}

func (m *tokenManager) LoggedIn() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ts.AccessToken != "" || m.ts.RefreshToken != ""
}

// loadPersisted 启动时从磁盘恢复令牌（仅当平台加密可用且解密成功）。
func (m *tokenManager) loadPersisted() {
	path, err := passportTokenPath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var wrapper struct {
		Protected string `json:"protected"`
	}
	if json.Unmarshal(data, &wrapper) != nil || wrapper.Protected == "" {
		return
	}
	plain, ok := unprotectSecret(wrapper.Protected)
	if !ok {
		return
	}
	var ts persistedTokens
	if json.Unmarshal([]byte(plain), &ts) != nil {
		return
	}
	m.setInMemory(tokenSet{AccessToken: ts.Access, RefreshToken: ts.Refresh, Expiry: ts.Expiry})
}

// persistedTokens 是落盘明文（加密前）的形状。
type persistedTokens struct {
	Access  string    `json:"access"`
	Refresh string    `json:"refresh"`
	Expiry  time.Time `json:"expiry"`
}

// passportTokenPath 与 desktop.json 同目录的令牌文件。
func passportTokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runcode", "passport.json"), nil
}

// persistTokens 加密落盘；平台无加密（非 Windows 的 no-op）时不落盘——
// 令牌只活在内存，重启需重新登录（本期约束）。失败非致命。
func persistTokens(ts tokenSet) {
	plain, err := json.Marshal(persistedTokens{Access: ts.AccessToken, Refresh: ts.RefreshToken, Expiry: ts.Expiry})
	if err != nil {
		return
	}
	protected, ok := protectSecret(string(plain))
	if !ok {
		return
	}
	path, err := passportTokenPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(map[string]string{"protected": protected})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop/ -run 'TestToken' -v` → PASS（4 用例）。
注意：`TestTokenRefreshesNearExpiry` 会真实落盘到 `%AppData%\runcode\passport.json`（DPAPI 成功时）——测试后无碍，但若介意可在测试里先 `t.Setenv("APPDATA", t.TempDir())` 隔离（Windows 的 `os.UserConfigDir` 读 APPDATA）。推荐加上。

- [ ] **Step 5: Commit**

```powershell
git add internal/desktop/tokens.go internal/desktop/tokens_test.go
git commit -m "desktop: tokenManager 静默续期 + DPAPI 令牌落盘"
```

---

### Task 5: passport.go——配置、登录编排、绑定方法、引擎接线

**Files:**
- Create: `internal/desktop/passport.go`
- Modify: `internal/desktop/types.go:19-42`（事件常量）
- Modify: `internal/desktop/app.go:46-97`（App 字段 + New + StartSession 接线）
- Test: `internal/desktop/passport_test.go`

**Interfaces:**
- Consumes: Task 2/3/4 全部；`engine.Config.TokenSource`（Task 1）；现有 `EventSink`
- Produces（前端消费）:
  - 事件 `EventPassportChanged = "passport:changed"`，payload `PassportStatus`
  - `type PassportStatus struct { LoggedIn bool; UserID, UserName, Name, Nickname, Avatar, TenantID string }`（json 小驼峰）
  - `type PassportModel struct { ID string `json:"id"`; OwnedBy string `json:"ownedBy"` }`
  - App 方法：`PassportStatus() PassportStatus`、`PassportLogin() (PassportStatus, error)`、`PassportCancelLogin()`、`PassportLogout()`、`PassportModels() ([]PassportModel, error)`
  - `provider == "passport"` 的 StartSessionRequest → openai provider + Bridge baseURL + TokenSource

- [ ] **Step 1: 写失败测试**

`internal/desktop/passport_test.go`：

```go
package desktop

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPassportConfigDefaultsAndEnvOverride(t *testing.T) {
	t.Setenv("RUNCODE_PASSPORT_AUTHORITY", "")
	t.Setenv("RUNCODE_PASSPORT_CLIENT_ID", "")
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", "")
	cfg := passportConfig()
	if cfg.Authority != "https://passport-ai.ouchn.edu.cn" {
		t.Fatalf("authority = %q", cfg.Authority)
	}
	if cfg.ClientID != "runcode-desktop" {
		t.Fatalf("clientID = %q", cfg.ClientID)
	}
	if cfg.BridgeBaseURL != "http://localhost:8199" {
		t.Fatalf("bridge = %q", cfg.BridgeBaseURL)
	}
	if cfg.RedirectURI != "http://localhost:8199/oauth/callback" {
		t.Fatalf("redirect = %q", cfg.RedirectURI)
	}

	t.Setenv("RUNCODE_BRIDGE_BASE_URL", "https://bridge.example/")
	cfg = passportConfig()
	if cfg.BridgeBaseURL != "https://bridge.example" || cfg.RedirectURI != "https://bridge.example/oauth/callback" {
		t.Fatalf("override: %+v", cfg)
	}
}

func TestApplyPassportProvider(t *testing.T) {
	app := New(recordingSink())
	app.tokens.setInMemory(tokenSet{AccessToken: "AT", Expiry: time.Now().Add(time.Hour)})

	req := StartSessionRequest{CWD: t.TempDir(), Provider: "passport", Model: "qwen-max"}
	cfg, err := buildConfig(req)
	if err != nil {
		t.Fatal(err)
	}
	cfg = app.applyPassport(cfg, req)
	if cfg.Provider != "openai" {
		t.Fatalf("provider = %q, want openai", cfg.Provider)
	}
	if cfg.BaseURL != passportConfig().BridgeBaseURL+"/v1" {
		t.Fatalf("baseURL = %q", cfg.BaseURL)
	}
	if cfg.TokenSource == nil {
		t.Fatal("TokenSource must be wired")
	}
	if tok, err := cfg.TokenSource(); err != nil || tok != "AT" {
		t.Fatalf("TokenSource() = %q, %v", tok, err)
	}
}

func TestPassportModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer AT" {
			t.Fatalf("auth = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen-max","owned_by":"qwen"},{"id":"glm-4","owned_by":"zhipu"}]}`))
	}))
	defer srv.Close()
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", srv.URL)

	app := New(recordingSink())
	app.tokens.setInMemory(tokenSet{AccessToken: "AT", Expiry: time.Now().Add(time.Hour)})
	models, err := app.PassportModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "qwen-max" || models[1].OwnedBy != "zhipu" {
		t.Fatalf("models = %+v", models)
	}
}

func TestPassportLogoutClearsAndEmits(t *testing.T) {
	sink := recordingSink()
	app := New(sink)
	app.tokens.setInMemory(tokenSet{AccessToken: "AT", Expiry: time.Now().Add(time.Hour)})
	app.PassportLogout()
	if app.tokens.LoggedIn() {
		t.Fatal("tokens must be cleared")
	}
	if !sink.has(EventPassportChanged) {
		t.Fatal("passport:changed must be emitted")
	}
}
```

`recordingSink` 若测试包中已有等价 fake（`grep -n "EventSink" internal/desktop/*_test.go` 先查），用现有的；没有则在 `passport_test.go` 里加：

```go
type recSink struct {
	mu     sync.Mutex
	events []string
}

func recordingSink() *recSink { return &recSink{} }
func (s *recSink) Emit(event string, data any) {
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
}
func (s *recSink) has(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e == name {
			return true
		}
	}
	return false
}
```

（需 import `sync`；若与现有 fake 重名冲突，以现有为准改测试。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop/ -run Passport -v` → 编译错误（`passportConfig` 等未定义）。

- [ ] **Step 3: 实现**

`internal/desktop/types.go`：事件常量块（19-42 行）追加：

```go
	// EventPassportChanged carries a PassportStatus whenever login state changes
	// (login success, logout, or refresh-token expiry forcing re-login).
	EventPassportChanged = "passport:changed"
```

`internal/desktop/passport.go`：

```go
package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/engine"
)

// Passport（通行证）登录：系统浏览器 + authorization_code + PKCE。
// redirect_uri 恒为 Bridge 的中转端点，本地端口经 state 传递（Bridge 302 回跳）。

// passportSettings 是登录与 Bridge 访问的端点配置，默认值为 AI 环境实测值，
// 均可用环境变量覆盖。
type passportSettings struct {
	Authority     string // 如 https://passport-ai.ouchn.edu.cn
	ClientID      string
	Scopes        string
	BridgeBaseURL string // 如 http://localhost:8199（不含 /v1）
	RedirectURI   string // BridgeBaseURL + /oauth/callback
}

func passportConfig() passportSettings {
	authority := strings.TrimRight(envOr("RUNCODE_PASSPORT_AUTHORITY", "https://passport-ai.ouchn.edu.cn"), "/")
	bridge := strings.TrimRight(envOr("RUNCODE_BRIDGE_BASE_URL", "http://localhost:8199"), "/")
	return passportSettings{
		Authority:     authority,
		ClientID:      envOr("RUNCODE_PASSPORT_CLIENT_ID", "runcode-desktop"),
		Scopes:        "openid profile offline_access passportapi",
		BridgeBaseURL: bridge,
		RedirectURI:   bridge + "/oauth/callback",
	}
}

func (s passportSettings) tokenURL() string { return s.Authority + "/connect/token" }

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// PassportStatus 是前端展示用的登录态 + 用户信息。
type PassportStatus struct {
	LoggedIn bool   `json:"loggedIn"`
	UserID   string `json:"userId,omitempty"`
	UserName string `json:"userName,omitempty"`
	Name     string `json:"name,omitempty"`
	Nickname string `json:"nickname,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
	TenantID string `json:"tenantId,omitempty"`
}

// PassportModel 是 Bridge /v1/models 列表项。
type PassportModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"ownedBy"`
}

// loginTimeout 是等待用户在浏览器完成登录的上限。
const loginTimeout = 5 * time.Minute

// PassportStatus 返回当前登录态；已登录且未缓存用户信息时尝试拉取（失败不阻断）。
func (a *App) PassportStatus() PassportStatus {
	if !a.tokens.LoggedIn() {
		return PassportStatus{}
	}
	a.mu.Lock()
	cached := a.passportUser
	a.mu.Unlock()
	if cached != nil {
		return *cached
	}
	st := PassportStatus{LoggedIn: true}
	if me, err := a.fetchMe(); err == nil {
		st = me
	}
	a.mu.Lock()
	a.passportUser = &st
	a.mu.Unlock()
	return st
}

// PassportLogin 执行完整登录流程，阻塞至完成/超时/取消。
func (a *App) PassportLogin() (PassportStatus, error) {
	cfg := passportConfig()

	cs, err := startCallbackServer()
	if err != nil {
		return PassportStatus{}, err
	}
	defer cs.Close()

	verifier, challenge, err := genPKCE()
	if err != nil {
		return PassportStatus{}, err
	}
	state, err := genState(cs.Port)
	if err != nil {
		return PassportStatus{}, err
	}
	cs.ExpectState(state)

	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	a.mu.Lock()
	a.loginCancel = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.loginCancel = nil
		a.mu.Unlock()
	}()

	authURL := buildAuthorizeURL(cfg.Authority, cfg.ClientID, cfg.RedirectURI, cfg.Scopes, state, challenge)
	if err := openBrowser(authURL); err != nil {
		return PassportStatus{}, fmt.Errorf("无法打开系统浏览器: %w", err)
	}

	var code string
	select {
	case r := <-cs.Result:
		if r.Err != nil {
			return PassportStatus{}, r.Err
		}
		code = r.Code
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.Canceled) {
			return PassportStatus{}, errors.New("登录已取消")
		}
		return PassportStatus{}, errors.New("登录超时，请重试")
	}

	ts, err := exchangeCode(ctx, passportHTTP(), cfg.tokenURL(), cfg.ClientID, code, verifier, cfg.RedirectURI)
	if err != nil {
		return PassportStatus{}, err
	}
	a.tokens.Set(ts)

	st := PassportStatus{LoggedIn: true}
	if me, err := a.fetchMe(); err == nil {
		st = me
	}
	a.mu.Lock()
	a.passportUser = &st
	a.mu.Unlock()
	a.sink.Emit(EventPassportChanged, st)
	return st, nil
}

// PassportCancelLogin 取消进行中的登录等待。
func (a *App) PassportCancelLogin() {
	a.mu.Lock()
	cancel := a.loginCancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// PassportLogout 清除本地令牌（不调 Passport endsession——桌面登出不应
// 顺带登出浏览器里的 SSO 会话）。
func (a *App) PassportLogout() {
	a.tokens.Clear()
	a.mu.Lock()
	a.passportUser = nil
	a.mu.Unlock()
	a.sink.Emit(EventPassportChanged, PassportStatus{})
}

// PassportModels 经 Bridge 列平台模型。
func (a *App) PassportModels() ([]PassportModel, error) {
	body, err := a.bridgeGet("/v1/models")
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("解析模型列表失败: %w", err)
	}
	models := make([]PassportModel, 0, len(payload.Data))
	for _, m := range payload.Data {
		models = append(models, PassportModel{ID: m.ID, OwnedBy: m.OwnedBy})
	}
	return models, nil
}

// fetchMe 经 Bridge /api/me 拉取用户信息。
func (a *App) fetchMe() (PassportStatus, error) {
	body, err := a.bridgeGet("/api/me")
	if err != nil {
		return PassportStatus{}, err
	}
	var me struct {
		UserID   string `json:"userId"`
		UserName string `json:"userName"`
		Name     string `json:"name"`
		Nickname string `json:"nickname"`
		Avatar   string `json:"avatar"`
		TenantID string `json:"tenantId"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		return PassportStatus{}, err
	}
	return PassportStatus{LoggedIn: true, UserID: me.UserID, UserName: me.UserName,
		Name: me.Name, Nickname: me.Nickname, Avatar: me.Avatar, TenantID: me.TenantID}, nil
}

// bridgeGet 带登录令牌 GET Bridge 端点，返回响应体。
func (a *App) bridgeGet(path string) ([]byte, error) {
	tok, err := a.tokens.Token()
	if err != nil {
		return nil, err
	}
	cfg := passportConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.BridgeBaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := passportHTTP().Do(req)
	if err != nil {
		return nil, fmt.Errorf("访问中间服务失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("中间服务返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// applyPassport 在 provider=="passport" 时把引擎配置切到 Bridge + TokenSource。
func (a *App) applyPassport(cfg engine.Config, req StartSessionRequest) engine.Config {
	if !strings.EqualFold(strings.TrimSpace(req.Provider), "passport") {
		return cfg
	}
	pc := passportConfig()
	cfg.Provider = "openai"
	cfg.BaseURL = pc.BridgeBaseURL + "/v1"
	cfg.APIKey = ""
	cfg.AuthToken = ""
	cfg.TokenSource = a.tokens.Token
	return cfg
}

// passportHTTP 是调 Passport/Bridge 的普通客户端。不用 internal/webclient——
// 那个加固客户端拒连回环/内网地址，而 Bridge 常部署在内网。
func passportHTTP() *http.Client {
	return &http.Client{Timeout: 60 * time.Second}
}

// openBrowser 用系统默认浏览器打开 URL（登录页）。
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// rundll32 是打开默认浏览器的稳妥方式（explorer 对带 query 的 URL 处理不一致）
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
```

`internal/desktop/app.go`：
1. `App` 结构体（46-70 行）加字段：

```go
	// tokens holds the Passport OAuth tokens (memory + DPAPI persistence); it is
	// also the engine's TokenSource. passportUser caches /api/me for PassportStatus.
	// loginCancel cancels an in-flight browser login wait.
	tokens       *tokenManager
	passportUser *PassportStatus
	loginCancel  context.CancelFunc
```

2. `New`（73-75 行）改为：

```go
func New(sink EventSink) *App {
	a := &App{sink: sink, edits: newEditStore()}
	pc := passportConfig()
	a.tokens = newTokenManager(pc.tokenURL(), pc.ClientID, passportHTTP(), func() {
		a.mu.Lock()
		a.passportUser = nil
		a.mu.Unlock()
		sink.Emit(EventPassportChanged, PassportStatus{})
	})
	a.tokens.loadPersisted()
	return a
}
```

3. `StartSession`（82-97 行）在 `buildConfig` 后加一行：

```go
	cfg, err := buildConfig(req)
	if err != nil {
		return SessionInfo{}, err
	}
	cfg = a.applyPassport(cfg, req)
```

**注意**：`buildConfig` 要求 model 非空（`config.go:34`）——passport 模式下模型来自下拉，前端必传；provider=="passport" 时 `engine.Config.Provider` 先被 buildConfig 原样填成 "passport"，`applyPassport` 再改写为 "openai"，顺序正确。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop/ -run Passport -v` → PASS（4 用例）。
Run: `go test ./internal/desktop/` → 全绿（现有测试无回归；若现有测试用自己的 sink fake 与 recSink 冲突，按现有命名调整）。

- [ ] **Step 5: Commit**

```powershell
git add internal/desktop/passport.go internal/desktop/passport_test.go internal/desktop/types.go internal/desktop/app.go
git commit -m "desktop: 通行证登录编排 + Bridge 模型列表 + 引擎 TokenSource 接线"
```

---

### Task 6: 自定义模型（持久化 + App 方法）

**Files:**
- Create: `internal/desktop/custommodels.go`
- Modify: `internal/desktop/types.go:68-108`（StartSessionRequest 加 CustomModels 字段）
- Modify: `internal/desktop/store.go:92-116`（protect/unprotect 覆盖自定义模型的 key）
- Test: `internal/desktop/custommodels_test.go`

**Interfaces:**
- Produces（前端消费）:
  - `type CustomModel struct { Name, Model, BaseURL, APIKey string; APIKeyProtected string }`（json 小驼峰，`apiKeyProtected,omitempty`）
  - App 方法：`ListCustomModels() []CustomModel`（APIKey 已解密）、`SaveCustomModel(m CustomModel) ([]CustomModel, error)`（同名覆盖）、`DeleteCustomModel(name string) []CustomModel`
  - 自定义模型走现有直连路径：前端选中后把 `{provider:"openai", model, baseURL, apiKey}` 填进 StartSessionRequest，后端无需新逻辑

- [ ] **Step 1: 写失败测试**

`internal/desktop/custommodels_test.go`：

```go
package desktop

import "testing"

func TestCustomModelsCRUDRoundTrip(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir()) // 隔离 desktop.json（Windows: os.UserConfigDir 读 APPDATA）

	app := New(recordingSink())
	if got := app.ListCustomModels(); len(got) != 0 {
		t.Fatalf("initial = %+v, want empty", got)
	}

	list, err := app.SaveCustomModel(CustomModel{Name: "本地 Ollama", Model: "qwen2.5-coder", BaseURL: "http://localhost:11434/v1", APIKey: "sk-local"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "本地 Ollama" {
		t.Fatalf("after save = %+v", list)
	}

	// 重新 List：APIKey 应解密可读（Windows DPAPI 往返）
	got := app.ListCustomModels()
	if len(got) != 1 || got[0].APIKey != "sk-local" || got[0].APIKeyProtected != "" {
		t.Fatalf("list = %+v, want decrypted key", got)
	}

	// 同名覆盖
	list, err = app.SaveCustomModel(CustomModel{Name: "本地 Ollama", Model: "llama3", BaseURL: "http://localhost:11434/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Model != "llama3" {
		t.Fatalf("after overwrite = %+v", list)
	}

	if list = app.DeleteCustomModel("本地 Ollama"); len(list) != 0 {
		t.Fatalf("after delete = %+v", list)
	}
}

func TestSaveCustomModelValidates(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(recordingSink())
	if _, err := app.SaveCustomModel(CustomModel{Name: "", Model: "m"}); err == nil {
		t.Fatal("want error for empty name")
	}
	if _, err := app.SaveCustomModel(CustomModel{Name: "n", Model: ""}); err == nil {
		t.Fatal("want error for empty model")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop/ -run CustomModel -v` → 编译错误。

- [ ] **Step 3: 实现**

`internal/desktop/types.go`：`StartSessionRequest`（RecentWorkspaces 字段之后）追加：

```go
	// CustomModels is the user-defined direct-connection model list, maintained
	// backend-side (Save/Delete methods); values sent by the frontend are ignored.
	// API keys are DPAPI-protected at rest like the top-level credentials.
	CustomModels []CustomModel `json:"customModels,omitempty"`
```

`internal/desktop/custommodels.go`：

```go
package desktop

import (
	"errors"
	"strings"
)

// CustomModel 是用户自定义的直连模型接入点（OpenAI 兼容），与通行证平台模型
// 并列显示在模型选择器里；选中后按老的直连路径起会话。
type CustomModel struct {
	Name    string `json:"name"`
	Model   string `json:"model"`
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey,omitempty"`
	// APIKeyProtected 是 DPAPI 加密后的落盘形态（同 StartSessionRequest 的凭证）。
	APIKeyProtected string `json:"apiKeyProtected,omitempty"`
}

// ListCustomModels 返回解密后的自定义模型列表（给表单编辑/起会话用）。
func (a *App) ListCustomModels() []CustomModel {
	raw := loadRawConfig().CustomModels
	out := make([]CustomModel, 0, len(raw))
	for _, m := range raw {
		out = append(out, unprotectCustomModel(m))
	}
	return out
}

// SaveCustomModel 新增或同名覆盖一个自定义模型，返回最新列表（已解密）。
func (a *App) SaveCustomModel(m CustomModel) ([]CustomModel, error) {
	m.Name = strings.TrimSpace(m.Name)
	m.Model = strings.TrimSpace(m.Model)
	if m.Name == "" {
		return nil, errors.New("模型名称不能为空")
	}
	if m.Model == "" {
		return nil, errors.New("模型 ID 不能为空")
	}
	cfg := loadRawConfig()
	next := make([]CustomModel, 0, len(cfg.CustomModels)+1)
	for _, existing := range cfg.CustomModels {
		if existing.Name != m.Name {
			next = append(next, existing)
		}
	}
	next = append(next, protectCustomModel(m))
	cfg.CustomModels = next
	saveRawConfig(cfg)
	return a.ListCustomModels(), nil
}

// DeleteCustomModel 按名称删除，返回最新列表。
func (a *App) DeleteCustomModel(name string) []CustomModel {
	cfg := loadRawConfig()
	next := cfg.CustomModels[:0]
	for _, m := range cfg.CustomModels {
		if m.Name != name {
			next = append(next, m)
		}
	}
	cfg.CustomModels = next
	saveRawConfig(cfg)
	return a.ListCustomModels()
}

func protectCustomModel(m CustomModel) CustomModel {
	m.APIKeyProtected, _ = protectSecret(m.APIKey)
	m.APIKey = ""
	return m
}

func unprotectCustomModel(m CustomModel) CustomModel {
	if m.APIKeyProtected != "" {
		if s, ok := unprotectSecret(m.APIKeyProtected); ok {
			m.APIKey = s
		}
	}
	m.APIKeyProtected = ""
	return m
}
```

`internal/desktop/store.go` 追加（`loadRawConfig` 之后）——直接持久化 raw 配置（自定义模型自带加密处理，不经过 `protectRequestSecrets`，避免把顶层凭证字段清掉）：

```go
// saveRawConfig writes the raw persisted request back as-is (used by the
// custom-model CRUD, which manages its own field encryption). Failures are
// non-fatal, mirroring saveConfig.
func saveRawConfig(req StartSessionRequest) {
	path, err := desktopConfigPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}
```

**同时改 `saveConfig`（store.go:78-80）**：与 RecentWorkspaces 同理，CustomModels 是服务端所有的字段，saveConfig 必须从磁盘 carry forward，忽略前端回显：

```go
	prev := loadRawConfig()
	req.RecentWorkspaces = mergeRecentWorkspaces(prev.RecentWorkspaces, req.CWD)
	req.CustomModels = prev.CustomModels
	req = protectRequestSecrets(req)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop/ -run CustomModel -v` → PASS。
Run: `go test ./internal/desktop/` → 全绿。

- [ ] **Step 5: Commit**

```powershell
git add internal/desktop/custommodels.go internal/desktop/custommodels_test.go internal/desktop/types.go internal/desktop/store.go
git commit -m "desktop: 自定义模型 CRUD（DPAPI 加密落盘，与平台模型并存）"
```

---

### Task 7: 前端桥接（bridge.ts + wails.d.ts）

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/bridge.ts`（类型 + 包装 + Events）
- Modify: `cmd/runcode-desktop/frontend/src/wails.d.ts:13-49`（方法声明）

**Interfaces:**
- Consumes: Task 5/6 的 Go 绑定方法
- Produces（Task 8 消费）: `PassportStatus`、`PassportModel`、`CustomModel` TS 接口；`passportStatus()`、`passportLogin()`、`passportCancelLogin()`、`passportLogout()`、`passportModels()`、`listCustomModels()`、`saveCustomModel(m)`、`deleteCustomModel(name)`；`Events.PassportChanged`

- [ ] **Step 1: bridge.ts 追加类型（`ResumedSession` 接口之后、`const app` 之前）**

```ts
export interface PassportStatus {
  loggedIn: boolean
  userId?: string
  userName?: string
  name?: string
  nickname?: string
  avatar?: string
  tenantId?: string
}

export interface PassportModel {
  id: string
  ownedBy: string
}

export interface CustomModel {
  name: string
  model: string
  baseURL: string
  apiKey?: string
}
```

- [ ] **Step 2: bridge.ts 追加包装函数（`saveSettings` 之后）**

```ts
export const passportStatus = () => app().PassportStatus() as Promise<PassportStatus>
export const passportLogin = () => app().PassportLogin() as Promise<PassportStatus>
export const passportCancelLogin = () => app().PassportCancelLogin()
export const passportLogout = () => app().PassportLogout()
export const passportModels = () => app().PassportModels() as Promise<PassportModel[] | null>
export const listCustomModels = () => app().ListCustomModels() as Promise<CustomModel[] | null>
export const saveCustomModel = (m: CustomModel) => app().SaveCustomModel(m) as Promise<CustomModel[] | null>
export const deleteCustomModel = (name: string) => app().DeleteCustomModel(name) as Promise<CustomModel[] | null>
```

`Events` 常量表加：`PassportChanged: 'passport:changed',`

- [ ] **Step 3: wails.d.ts 的 App 接口内追加**

```ts
          PassportStatus(): Promise<unknown>
          PassportLogin(): Promise<unknown>
          PassportCancelLogin(): Promise<void>
          PassportLogout(): Promise<void>
          PassportModels(): Promise<unknown>
          ListCustomModels(): Promise<unknown>
          SaveCustomModel(m: unknown): Promise<unknown>
          DeleteCustomModel(name: string): Promise<unknown>
```

- [ ] **Step 4: 验证编译**

Run: `cd cmd/runcode-desktop/frontend && npm run build`
Expected: 构建成功（新代码尚无调用方，仅类型检查）。

- [ ] **Step 5: Commit**

```powershell
git add cmd/runcode-desktop/frontend/src/bridge.ts cmd/runcode-desktop/frontend/src/wails.d.ts
git commit -m "Desktop UI: 通行证/自定义模型的 bridge 类型与包装"
```

---

### Task 8: StartForm 登录优先改造

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/pages.tsx:1144-1254`（StartForm）

**Interfaces:**
- Consumes: Task 7 的全部包装函数与类型
- Produces: 登录区（未登录=主按钮；已登录=用户名+登出）；模型选择器（平台模型 optgroup + 自定义模型 optgroup + 手动配置项）；自定义模型管理折叠块；`onStart` 请求映射（平台→`provider:'passport'`；自定义→`provider:'openai'`+其 baseURL/key；手动→现状）

- [ ] **Step 1: 改造 StartForm**

在 `StartForm` 组件内（`const [cwd, setCwd] = ...` 之后）加状态与加载逻辑：

```tsx
  const [passport, setPassport] = useState<PassportStatus>({ loggedIn: false })
  const [platformModels, setPlatformModels] = useState<PassportModel[]>([])
  const [customModels, setCustomModels] = useState<CustomModel[]>([])
  const [loggingIn, setLoggingIn] = useState(false)
  const [passportError, setPassportError] = useState('')
  // modelChoice: 'passport:<id>' | 'custom:<name>' | 'manual'
  const [modelChoice, setModelChoice] = useState(initial.provider === 'passport' && initial.model ? `passport:${initial.model}` : 'manual')
  const [showCustomEditor, setShowCustomEditor] = useState(false)
  const [cmName, setCmName] = useState('')
  const [cmModel, setCmModel] = useState('')
  const [cmBaseURL, setCmBaseURL] = useState('')
  const [cmApiKey, setCmApiKey] = useState('')

  const refreshPassport = async () => {
    try {
      const st = await passportStatus()
      setPassport(st)
      if (st.loggedIn) setPlatformModels((await passportModels()) ?? [])
    } catch { /* Bridge 不可达时留空，界面提示 */ }
    try { setCustomModels((await listCustomModels()) ?? []) } catch { /* ignore */ }
  }
  useEffect(() => {
    void refreshPassport()
    return onEvent<PassportStatus>(Events.PassportChanged, (st) => {
      setPassport(st)
      if (!st.loggedIn) { setPlatformModels([]); setModelChoice('manual') }
      else void refreshPassport()
    })
  }, [])

  const doLogin = async () => {
    setLoggingIn(true); setPassportError('')
    try {
      await passportLogin()
      await refreshPassport()
    } catch (e) {
      setPassportError(String(e))
    } finally { setLoggingIn(false) }
  }
```

（文件顶部 import 需补：`passportStatus, passportLogin, passportLogout, passportModels, listCustomModels, saveCustomModel, deleteCustomModel, onEvent, Events` 及类型 `PassportStatus, PassportModel, CustomModel`——按 pages.tsx 现有 import bridge 的行追加。）

- [ ] **Step 2: 登录区 JSX（标题块 `</div>` 之后、"工作区目录"之前插入）**

```tsx
        {passport.loggedIn ? (
          <div className="flex items-center justify-between rounded-[9px] border border-line2 bg-surface2 px-3 py-2.5">
            <span className="text-[13px]">已登录：<b>{passport.name || passport.userName || passport.userId}</b></span>
            <button type="button" className="text-[12px] text-muted hover:text-ink" onClick={() => { void passportLogout() }}>登出</button>
          </div>
        ) : (
          <div className="flex flex-col gap-1.5">
            <button type="button" className={`${BTN} ${BTN_PRIMARY} py-2.5`} disabled={loggingIn} onClick={() => void doLogin()}>
              {loggingIn ? '等待浏览器登录…' : '通行证登录'}
            </button>
            {loggingIn && <button type="button" className="text-[12px] text-muted" onClick={() => void passportCancelLogin()}>取消</button>}
            {passportError && <div className="text-red text-[12.5px]">{passportError}</div>}
          </div>
        )}
```

- [ ] **Step 3: 模型选择器（替换现有"模型"input 为选择器 + 手动回退）**

把 1217 行的模型 `<label>` 换成：

```tsx
          <label className={label}>模型
            <select className={field} value={modelChoice} onChange={(e) => setModelChoice(e.target.value)}>
              {passport.loggedIn && platformModels.length > 0 && (
                <optgroup label="平台模型（通行证）">
                  {platformModels.map((m) => <option key={m.id} value={`passport:${m.id}`}>{m.id}（{m.ownedBy}）</option>)}
                </optgroup>
              )}
              {customModels.length > 0 && (
                <optgroup label="自定义模型">
                  {customModels.map((m) => <option key={m.name} value={`custom:${m.name}`}>{m.name}</option>)}
                </optgroup>
              )}
              <option value="manual">手动配置…</option>
            </select>
          </label>
```

`modelChoice !== 'manual'` 时隐藏服务商/Base URL/API 密钥三个字段（用条件渲染包住现有 JSX：`{modelChoice === 'manual' && (<>…原有三个字段…</>)}`；手动模型名 input 同理保留在 manual 分支里，独立 state `manualModel` 初始化自 `initial.model`）。

- [ ] **Step 4: 自定义模型管理折叠块（Base URL 字段之后插入）**

```tsx
        <div className={label}>
          <button type="button" className="text-left text-[12.5px] text-muted hover:text-ink" onClick={() => setShowCustomEditor(!showCustomEditor)}>
            {showCustomEditor ? '▾' : '▸'} 自定义模型管理（{customModels.length}）
          </button>
          {showCustomEditor && (
            <div className="flex flex-col gap-2 rounded-[9px] border border-line2 p-3">
              {customModels.map((m) => (
                <div key={m.name} className="flex items-center justify-between text-[12.5px]">
                  <span className="truncate">{m.name} <span className="text-muted">({m.model})</span></span>
                  <button type="button" className="text-muted hover:text-red" onClick={async () => setCustomModels((await deleteCustomModel(m.name)) ?? [])}>删除</button>
                </div>
              ))}
              <input className={field} placeholder="显示名称（如 本地 Ollama）" value={cmName} onChange={(e) => setCmName(e.target.value)} />
              <div className="grid grid-cols-2 gap-2">
                <input className={field} placeholder="模型 ID" value={cmModel} onChange={(e) => setCmModel(e.target.value)} />
                <input className={field} placeholder="Base URL" value={cmBaseURL} onChange={(e) => setCmBaseURL(e.target.value)} />
              </div>
              <input className={field} type="password" placeholder="API 密钥（可空）" value={cmApiKey} onChange={(e) => setCmApiKey(e.target.value)} />
              <button type="button" className={BTN} disabled={!cmName.trim() || !cmModel.trim()} onClick={async () => {
                const list = await saveCustomModel({ name: cmName.trim(), model: cmModel.trim(), baseURL: cmBaseURL.trim(), apiKey: cmApiKey })
                setCustomModels(list ?? []); setCmName(''); setCmModel(''); setCmBaseURL(''); setCmApiKey('')
              }}>保存自定义模型</button>
            </div>
          )}
        </div>
```

- [ ] **Step 5: onStart 请求映射（替换 1248 行按钮的 onClick 参数构造）**

```tsx
  const buildRequest = (): StartSessionRequest => {
    const base = { cwd, permissionMode, thinkingEffort, maxContextTokens, harmJudgeModel, harmJudgeVotes }
    if (modelChoice.startsWith('passport:')) {
      return { ...base, provider: 'passport', model: modelChoice.slice('passport:'.length) }
    }
    if (modelChoice.startsWith('custom:')) {
      const cm = customModels.find((m) => `custom:${m.name}` === modelChoice)
      if (cm) return { ...base, provider: 'openai', model: cm.model, baseURL: cm.baseURL, apiKey: cm.apiKey }
    }
    return { ...base, provider, model: manualModel, baseURL, apiKey }
  }
```

按钮 `disabled` 追加条件：选平台/自定义模型时无需 cwd 之外的校验；`onClick={() => onStart(buildRequest())}`。

- [ ] **Step 6: 验证编译 + 手动冒烟**

Run: `cd cmd/runcode-desktop/frontend && npm run build` → 成功。
Run: `go -C cmd/runcode-desktop build ./...` → 成功。

- [ ] **Step 7: Commit**

```powershell
git add cmd/runcode-desktop/frontend/src/pages.tsx
git commit -m "Desktop UI: StartForm 通行证登录优先 + 平台/自定义模型选择器"
```

---

### Task 9: 全量验证与端到端手动清单

**Files:** 无新文件（验证任务）

- [ ] **Step 1: 核心全量回归**

Run: `go test -race ./...`（仓库根）
Expected: 全绿。

- [ ] **Step 2: 桌面模块编译**

Run: `go -C cmd/runcode-desktop build ./...` → 成功。
Run: `cd cmd/runcode-desktop/frontend && npm run build` → 成功。

- [ ] **Step 3: 端到端手动验证（需本地起 Bridge 或可达的部署实例）**

前置：`ouconline-ai-bridge` 本地运行（`mvn spring-boot:run`，8199），Passport 已注册 `http://localhost:8199/oauth/callback`；或设 `RUNCODE_BRIDGE_BASE_URL` 指向部署实例。

清单（逐项确认）：
1. `wails dev`（或构建后运行）→ 启动页出现"通行证登录"按钮；
2. 点击登录 → 系统浏览器打开 Passport 登录页 → 完成登录 → 浏览器显示"登录成功" → 应用内显示"已登录：<姓名>"；
3. 模型下拉出现"平台模型（通行证）"分组且有内容；
4. 选平台模型 + 工作区 → 开始会话 → 发消息 → 流式回复正常（经 Bridge）；
5. 添加一个自定义模型 → 下拉出现"自定义模型"分组 → 选中可正常起会话（直连）；
6. 重启应用 → 登录态保持（DPAPI 恢复）、自定义模型仍在；
7. 登出 → 模型下拉回退到手动配置；再登录可恢复。

- [ ] **Step 4: 完成后（不自动执行）**

汇报验证结果，与用户确认后按 finishing-a-development-branch 流程决定分支去向。

---

## Self-Review 结果

- **Spec 覆盖**：TokenSource 核心扩展（Task 1）、PKCE+state+回环回调+浏览器拉起（Task 2/3/5）、令牌 DPAPI 落盘+静默续期+失败登出（Task 4）、`passport:changed` 事件（Task 5）、Bridge `/v1/models`+`/api/me`（Task 5）、`provider=passport` 引擎接线（Task 5）、自定义模型（Task 6）、前端桥接与 StartForm 登录优先（Task 7/8）、验证（Task 9）。spec 的"设置页账号区块"由 StartForm 登录区覆盖其功能（登录态展示+登出）——设置页重复入口按 YAGNI 暂缓，如需要是纯增量。
- **占位符扫描**：无 TBD；Task 2 对 `postTokenForm` 路径启发式给出了两种明确写法；Task 5 的 recordingSink 给出了"用现有 fake 优先"的明确指引与完整备用实现。
- **类型一致性**：`tokenSet`（Task 2 定义，3/4/5 消费）、`tokenManager.Token()` 与 `TokenSource func() (string, error)`（Task 1）签名一致、`PassportStatus/PassportModel/CustomModel` Go json 标签与 TS 接口字段一一对应（小驼峰）、`saveRawConfig`（Task 6 定义并消费）——已核对。
