// Package codexauth implements ChatGPT (Codex) 的设备码登录：用户在浏览器里输一
// 串码完成授权，客户端换到一对可自动续期的令牌，用于访问 ChatGPT 订阅背后的
// Codex 后端。
//
// 为什么是设备码而不是浏览器回调：这条流程不需要注册 redirect_uri，也不需要本地
// 起监听端口——桌面端(尤其是被防火墙管着的机器)少一处会坏的环节。通行证那条走的
// 是授权码 + PKCE + Bridge 中转(见 internal/desktop/passport.go)，两条互不影响。
//
// 流程(端点与参数照 OpenAI 的设备授权约定)：
//
//  1. POST .../deviceauth/usercode  {client_id}      → device_auth_id + user_code
//  2. 用户打开 VerificationURL，输入 user_code
//  3. POST .../deviceauth/token     {device_auth_id, user_code}
//     403/404 = 还没授权，继续轮询；410 = 码过期；200 = 拿到授权码
//  4. POST /oauth/token             grant_type=authorization_code
//     → access_token + refresh_token + id_token
//  5. chatgpt-account-id 从 id_token 的 claims 里取
//
// 第 3 步返回的 code_verifier 是**服务端**生成的：PKCE 在设备码流程里由服务端保管，
// 客户端只负责把它原样带进第 4 步，不自己生成 challenge。
package codexauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// ClientID 是 Codex CLI 的公开客户端标识。设备码流程不需要客户端密钥。
	ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

	// VerificationURL 是给用户看的页面：在这里输入 UserCode。
	VerificationURL = "https://auth.openai.com/codex/device"

	usercodeURL = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	pollURL     = "https://auth.openai.com/api/accounts/deviceauth/token"
	// tokenURL 是换/刷新令牌的端点。gosec 只按名字里的 "token" 猜，这里是公开地址不是凭据。
	tokenURL = "https://auth.openai.com/oauth/token" //nolint:gosec // 公开端点地址，非凭据
	// redirectURI 是设备码流程里服务端约定的固定值，换令牌时必须原样回带。
	redirectURI = "https://auth.openai.com/deviceauth/callback"

	// refreshScope 是刷新时要带的范围。少了它服务端会拒。
	refreshScope = "openid profile email"

	// userAgent 标识本客户端。上游按客户端身份放行模型，所以它与 Originator/
	// ClientVersion 是一组，不要只改其中一个。
	userAgent = "runcode-codex-oauth"

	// Originator 与 ClientVersion 是调 Codex 后端时必带的指纹头。ChatGPT 按这组
	// 身份决定放行哪些模型(新模型会要求更高的 minimal_client_version)，所以升级
	// 时两个一起动。
	Originator = "codex_work_desktop"
	// ClientVersion 是上面那组指纹里的版本号，与 Originator 同源、同步升级。
	ClientVersion = "0.153.4"

	// httpTimeout 是单次认证请求的上限。认证不是流式的，卡住就该尽快失败。
	httpTimeout = 30 * time.Second

	// defaultInterval / defaultExpiry 是服务端没给时的兜底(OpenAI 约定 15 分钟)。
	defaultInterval = 5 * time.Second
	defaultExpiry   = 15 * time.Minute
	// pollMargin 在服务端给的间隔上再加一点余量，避免边界上被判成轮询过快。
	pollMargin = 3 * time.Second
	// maxBodyBytes 限制认证响应的读取量。
	maxBodyBytes = 1 << 20
	// maxModelsBytes 单列：模型清单每个条目带着几十个字段，比认证响应大得多。
	maxModelsBytes = 4 << 20
)

// ErrAuthorizationPending 表示用户还没在浏览器里完成授权，应继续轮询。
var ErrAuthorizationPending = errors.New("用户尚未完成授权")

// ErrCodeExpired 表示这串用户码已经过期，需要重新发起登录。
var ErrCodeExpired = errors.New("登录码已过期，请重新登录")

// DeviceCode 是一次登录里要让用户去操作的东西。
type DeviceCode struct {
	// DeviceAuthID 是这次登录的服务端句柄，轮询时回带。
	DeviceAuthID string `json:"deviceAuthId"`
	// UserCode 是给用户抄进浏览器的那串码。
	UserCode string `json:"userCode"`
	// VerificationURL 是用户要打开的页面。
	VerificationURL string `json:"verificationUrl"`
	// Interval 是两次轮询之间至少要等的时间。
	Interval time.Duration `json:"-"`
	// ExpiresAt 是这串码的失效时刻。
	ExpiresAt time.Time `json:"expiresAt"`
}

// Tokens 是一次成功登录的产物。AccountID 是调 Codex 后端时要带的
// chatgpt-account-id，没有它上游不认这个订阅。
type Tokens struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	AccountID    string
	Email        string
}

// Client 发起并完成设备码登录。零值不可用，用 New 构造。
type Client struct {
	hc *http.Client
	// endpoints 可在测试里改写指向 httptest；生产用官方地址。
	usercode, poll, token string
}

// New 返回一个用 hc 发请求的登录客户端。hc 为 nil 时自建一个带超时的普通客户端
// ——**不要**用引擎那个加固客户端，认证端点在公网，而加固客户端的用途是另一回事。
func New(hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: httpTimeout}
	}
	return &Client{hc: hc, usercode: usercodeURL, poll: pollURL, token: tokenURL}
}

// StartDeviceFlow 取一串用户码。拿到后把 UserCode 显示给用户、把
// VerificationURL 打开，然后用 PollToken 等他完成。
func (c *Client) StartDeviceFlow(ctx context.Context) (DeviceCode, error) {
	body, _ := json.Marshal(map[string]string{"client_id": ClientID})
	var resp struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		Interval     any    `json:"interval"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := c.postJSON(ctx, c.usercode, body, &resp); err != nil {
		return DeviceCode{}, fmt.Errorf("取登录码失败: %w", err)
	}
	if strings.TrimSpace(resp.DeviceAuthID) == "" || strings.TrimSpace(resp.UserCode) == "" {
		return DeviceCode{}, errors.New("取登录码失败: 服务端未返回登录码")
	}
	expiry := defaultExpiry
	if resp.ExpiresIn > 0 {
		expiry = time.Duration(resp.ExpiresIn) * time.Second
	}
	return DeviceCode{
		DeviceAuthID:    resp.DeviceAuthID,
		UserCode:        resp.UserCode,
		VerificationURL: VerificationURL,
		Interval:        parseInterval(resp.Interval) + pollMargin,
		ExpiresAt:       time.Now().Add(expiry),
	}, nil
}

// PollOnce 查一次授权是否完成。未完成返回 ErrAuthorizationPending，码过期返回
// ErrCodeExpired——两者都是流程的正常状态，不是故障。
//
// 成功时服务端一次性给回授权码与它自己保管的 code_verifier(设备码流程里 PKCE
// 由服务端生成)，本方法接着把它们换成令牌，所以返回的已经是可用的 Tokens。
func (c *Client) PollOnce(ctx context.Context, dc DeviceCode) (Tokens, error) {
	body, _ := json.Marshal(map[string]string{
		"device_auth_id": dc.DeviceAuthID,
		"user_code":      dc.UserCode,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.poll, strings.NewReader(string(body)))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.hc.Do(req)
	if err != nil {
		return Tokens{}, fmt.Errorf("轮询授权状态失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))

	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusNotFound:
		return Tokens{}, ErrAuthorizationPending
	case http.StatusGone:
		return Tokens{}, ErrCodeExpired
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Tokens{}, fmt.Errorf("轮询授权状态失败: HTTP %d %s", resp.StatusCode, snippet(payload))
	}

	var ok struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.Unmarshal(payload, &ok); err != nil {
		return Tokens{}, fmt.Errorf("解析授权结果失败: %w", err)
	}
	if ok.AuthorizationCode == "" || ok.CodeVerifier == "" {
		return Tokens{}, errors.New("授权结果缺少授权码")
	}
	return c.exchange(ctx, ok.AuthorizationCode, ok.CodeVerifier)
}

// WaitForToken 按服务端给的间隔轮询直到授权完成、码过期或 ctx 取消。
func (c *Client) WaitForToken(ctx context.Context, dc DeviceCode) (Tokens, error) {
	interval := dc.Interval
	if interval <= 0 {
		interval = defaultInterval + pollMargin
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return Tokens{}, ctx.Err()
		case <-timer.C:
		}
		tokens, err := c.PollOnce(ctx, dc)
		switch {
		case err == nil:
			return tokens, nil
		case errors.Is(err, ErrAuthorizationPending):
			// 还没授权：等下一拍。到点仍未完成就按过期处理，免得无限等下去。
			if !dc.ExpiresAt.IsZero() && time.Now().After(dc.ExpiresAt) {
				return Tokens{}, ErrCodeExpired
			}
			timer.Reset(interval)
		default:
			return Tokens{}, err
		}
	}
}

// Refresh 用 refresh token 换一对新令牌。服务端不一定回带新的 refresh token，
// 这时沿用旧的——**调用方必须保留**，否则下一次刷新就没依据了。
func (c *Client) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {ClientID},
		"scope":         {refreshScope},
	}
	tokens, err := c.tokenRequest(ctx, form)
	if err != nil {
		return Tokens{}, err
	}
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = refreshToken
	}
	return tokens, nil
}

// exchange 用授权码换令牌。redirect_uri 必须是服务端约定的那个固定值。
func (c *Client) exchange(ctx context.Context, code, verifier string) (Tokens, error) {
	return c.tokenRequest(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {ClientID},
		"code_verifier": {verifier},
	})
}

func (c *Client) tokenRequest(ctx context.Context, form url.Values) (Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.token, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.hc.Do(req)
	if err != nil {
		return Tokens{}, fmt.Errorf("换取令牌失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Tokens{}, fmt.Errorf("换取令牌失败: HTTP %d %s", resp.StatusCode, snippet(payload))
	}

	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return Tokens{}, fmt.Errorf("解析令牌失败: %w", err)
	}
	if body.AccessToken == "" {
		return Tokens{}, errors.New("令牌响应缺少 access_token")
	}
	accountID, email := accountFromIDToken(body.IDToken)
	expiry := time.Time{}
	if body.ExpiresIn > 0 {
		expiry = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	}
	return Tokens{
		AccessToken:  body.AccessToken,
		RefreshToken: body.RefreshToken,
		Expiry:       expiry,
		AccountID:    accountID,
		Email:        email,
	}, nil
}

func (c *Client) postJSON(ctx context.Context, endpoint string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, snippet(payload))
	}
	return json.Unmarshal(payload, out)
}

// accountFromIDToken 从 id_token 里取 chatgpt-account-id 与邮箱。
//
// id_token 是 JWT，这里**只读 payload、不验签**：它是刚从 TLS 上换回来的，用途
// 也只是取一个上游要的请求头值，不是用来做授权判定的。真正的鉴权在 access_token
// 上，由上游校验。
//
// account id 有两处可能：顶层 chatgpt_account_id，或命名空间 claim
// "https://api.openai.com/auth" 里的同名字段。两处都看，取先有的那个。
func accountFromIDToken(idToken string) (accountID, email string) {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return "", ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var claims struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
		Email            string `json:"email"`
		OpenAIAuth       struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return "", ""
	}
	id := claims.ChatGPTAccountID
	if id == "" {
		id = claims.OpenAIAuth.ChatGPTAccountID
	}
	return id, claims.Email
}

// parseInterval 容忍服务端把间隔写成数字或字符串。取不到就用默认值。
func parseInterval(v any) time.Duration {
	switch n := v.(type) {
	case float64:
		if n > 0 {
			return time.Duration(n) * time.Second
		}
	case string:
		var secs float64
		if _, err := fmt.Sscanf(n, "%f", &secs); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultInterval
}

// snippet 截一小段响应正文进错误信息：够定位问题，又不至于把整页 HTML 灌进日志。
func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	const limit = 200
	if len(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit]) + "…"
}

// Model 是 ChatGPT 账号下可用的一个 Codex 模型。
type Model struct {
	// ID 是发请求时用的模型标识(上游称 slug)。
	ID string
	// DisplayName / Description 是给人看的，可能为空。
	DisplayName string
	Description string
}

// FetchModels 问出这个账号真正能用的模型。
//
// 为什么必须问而不能写死一张表：可用模型按账号、订阅档次与客户端版本变化，写死的
// 结果是用户填了一个上游根本不认的名字，得到一句"model is not supported"——而那句话
// 还未必透得出来。实测某个 ChatGPT 账号下只有 gpt-6-astra 一个，gpt-5-codex 直接被拒。
//
// 端点是 chatgpt.com/backend-api/codex/models，不是 OpenAI 兼容的 /v1/models；
// 它同样按 originator/version 这组客户端身份放行。
func (c *Client) FetchModels(ctx context.Context, baseURL, token, accountID string) ([]Model, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/models?client_version=" + url.QueryEscape(ClientVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("originator", Originator)
	req.Header.Set("version", ClientVersion)
	if strings.TrimSpace(accountID) != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取模型清单失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, maxModelsBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("获取模型清单失败: HTTP %d %s", resp.StatusCode, snippet(payload))
	}

	// 上游把清单放在 models 里；data 是 OpenAI 风格端点的写法，一并认下以防它改口径。
	var body struct {
		Models []modelEntry `json:"models"`
		Data   []modelEntry `json:"data"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, fmt.Errorf("解析模型清单失败: %w", err)
	}
	entries := body.Models
	if len(entries) == 0 {
		entries = body.Data
	}
	out := make([]Model, 0, len(entries))
	for _, e := range entries {
		id := strings.TrimSpace(firstNonBlank(e.Slug, e.ID))
		if id == "" {
			continue
		}
		out = append(out, Model{ID: id, DisplayName: strings.TrimSpace(e.DisplayName), Description: strings.TrimSpace(e.Description)})
	}
	return out, nil
}

// modelEntry 只取用得上的几个字段：上游那份 JSON 每个模型带着几十个键，全解出来
// 只会让这里跟着它的内部结构走。
type modelEntry struct {
	Slug        string `json:"slug"`
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

func firstNonBlank(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
