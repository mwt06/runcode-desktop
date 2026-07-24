package desktop

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
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

// buildAuthorizeURL 组装 Passport 授权端点 URL。scheme 非空时作为 scheme 参数
// 选择上游身份源（如统一认证的 OneOuchnPassport）；空则走基座通行证默认。
func buildAuthorizeURL(authority, clientID, redirectURI, scopes, state, challenge, scheme string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", scopes)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if scheme != "" {
		q.Set("scheme", scheme)
	}
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

// postTokenForm 向令牌端点 POST 表单数据。
// tokenURL 应为完整的 token 端点 URL（例如 https://passport.example/connect/token）。
func postTokenForm(ctx context.Context, hc *http.Client, tokenURL string, form url.Values) (tokenSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenSet{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return tokenSet{}, fmt.Errorf("请求令牌端点失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
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

// callbackResult 是一次授权回调的结果：code 或错误，二选一。
type callbackResult struct {
	Code string
	Err  error
}

// callbackServer 是登录期间临时监听 127.0.0.1 随机端口的一次性回调接收器。
// Bridge 的 /oauth/callback 会按 state 里的端口把浏览器 302 回跳到这里。
type callbackServer struct {
	Port   int
	Result <-chan callbackResult

	mu       sync.Mutex
	expected string
	done     bool
	srv      *http.Server
	result   chan callbackResult // writable end, internal
}

// startCallbackServer 绑定 127.0.0.1 的随机端口并开始服务 /callback。
// 调用方拿到端口后生成 state，再 ExpectState 告知校验值。
func startCallbackServer() (*callbackServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("绑定回环端口失败: %w", err)
	}
	result := make(chan callbackResult, 1)
	cs := &callbackServer{
		Port:   ln.Addr().(*net.TCPAddr).Port,
		Result: result,
		result: result,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", cs.handle)
	// ReadHeaderTimeout 与 preview_server.go 一致：只监听回环，但仍不给慢速请求头
	// 无限占住连接的机会（gosec G112）。
	cs.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
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
	// Check-and-set in one critical section to prevent race.
	if cs.done || cs.expected == "" || q.Get("state") != cs.expected {
		cs.mu.Unlock()
		http.Error(w, "state 校验失败", http.StatusBadRequest)
		return
	}
	cs.done = true
	cs.mu.Unlock()

	var result callbackResult
	if e := q.Get("error"); e != "" {
		desc := q.Get("error_description")
		result.Err = fmt.Errorf("授权失败: %s %s", e, desc)
	} else if code := q.Get("code"); code != "" {
		result.Code = code
	} else {
		result.Err = fmt.Errorf("回调缺少 code")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if result.Err != nil {
		_, _ = w.Write([]byte(callbackPage("✕", "#e5484d", "登录未完成", "授权未成功，请返回应用重试。")))
	} else {
		_, _ = w.Write([]byte(callbackPage("✓", "#2e6bff", "登录成功", "您可以关闭此页面，返回应用继续。")))
	}
	cs.result <- result
}

// callbackPage 渲染浏览器回调结果页。内容全部为静态常量拼接（图标/颜色/文案
// 均来自本函数的调用点），不反射任何请求参数，无注入面。
func callbackPage(mark, color, title, subtitle string) string {
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>` + title + `</title>
<meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
  font-family:'Segoe UI','Microsoft YaHei',sans-serif;
  background:linear-gradient(160deg,#eef3fb 0%,#f6f8fd 45%,#e8f0fc 100%);">
<div style="background:#fff;border-radius:20px;padding:56px 72px;text-align:center;
  box-shadow:0 18px 50px rgba(46,107,255,.12);">
  <div style="width:76px;height:76px;margin:0 auto 22px;border-radius:50%;
    display:flex;align-items:center;justify-content:center;color:#fff;font-size:38px;font-weight:700;
    background:linear-gradient(135deg,` + color + `,` + color + `cc);
    box-shadow:0 10px 26px ` + color + `40;">` + mark + `</div>
  <div style="font-size:22px;font-weight:700;color:#1c2b4a;letter-spacing:.04em;">` + title + `</div>
  <div style="margin-top:10px;font-size:14px;color:#7a8699;">` + subtitle + `</div>
  <div style="margin-top:26px;font-size:12px;color:#aab3c2;">智开AI · 统一认证</div>
</div>
</body></html>`
}

// Close 停止监听。可安全多次调用。
func (cs *callbackServer) Close() {
	_ = cs.srv.Close()
}
