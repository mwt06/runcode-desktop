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

	"github.com/wt68/runcode/engine"
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

// tenantPathPrefix 把选定的租户拼进 Bridge 路径前缀：非空 → /t/{tenantId}，
// 空 → ""（走令牌自带租户/兜底）。租户 ID 已由 Bridge 侧白名单校验，这里原样拼。
func tenantPathPrefix(tenantID string) string {
	if strings.TrimSpace(tenantID) == "" {
		return ""
	}
	return "/t/" + tenantID
}

// loginTimeout 是等待用户在浏览器完成登录的上限。
const loginTimeout = 5 * time.Minute

// PassportStatus 返回当前登录态；已登录且未缓存用户信息时尝试拉取（失败不缓存、下次重试）。
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
		a.mu.Lock()
		a.passportUser = &st
		a.mu.Unlock()
	}
	// 失败：返回临时占位（LoggedIn=true 无档案），不缓存 —— 下次调用会重试 fetchMe
	return st
}

// PassportLogin 执行完整登录流程，阻塞至完成/超时/取消。
func (a *App) PassportLogin() (PassportStatus, error) {
	// 防护并发登录：若已有登录流程进行中，拒绝新请求
	a.mu.Lock()
	if a.loginCancel != nil {
		a.mu.Unlock()
		return PassportStatus{}, wireError(errors.New("已有登录流程进行中，请先完成或取消"))
	}
	a.mu.Unlock()
	// 注意：此处与后续 loginCancel 赋值间存在 TOCTOU 窗口（UI 双击时无害）

	cfg := passportConfig()

	cs, err := startCallbackServer()
	if err != nil {
		return PassportStatus{}, wireError(err)
	}
	defer cs.Close()

	verifier, challenge, err := genPKCE()
	if err != nil {
		return PassportStatus{}, wireError(err)
	}
	state, err := genState(cs.Port)
	if err != nil {
		return PassportStatus{}, wireError(err)
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
		return PassportStatus{}, wireError(fmt.Errorf("无法打开系统浏览器: %w", err))
	}

	var code string
	select {
	case r := <-cs.Result:
		if r.Err != nil {
			return PassportStatus{}, wireError(r.Err)
		}
		code = r.Code
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.Canceled) {
			return PassportStatus{}, wireError(errors.New("登录已取消"))
		}
		return PassportStatus{}, wireError(errors.New("登录超时，请重试"))
	}

	ts, err := exchangeCode(ctx, passportHTTP(), cfg.tokenURL(), cfg.ClientID, code, verifier, cfg.RedirectURI)
	if err != nil {
		return PassportStatus{}, wireError(err)
	}
	a.tokens.Set(ts)

	st := PassportStatus{LoggedIn: true}
	if me, err := a.fetchMe(); err == nil {
		st = me
		a.mu.Lock()
		a.passportUser = &st
		a.mu.Unlock()
	}
	// 失败：返回临时占位（LoggedIn=true 无档案），不缓存 —— 下次调用会重试 fetchMe
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

// PassportTenants 列出当前用户可用的租户；单租户时前端自动选定，多租户让用户选。
func (a *App) PassportTenants() ([]PassportTenant, error) {
	body, err := a.bridgeGet("/api/tenants")
	if err != nil {
		return nil, wireError(err)
	}
	var tenants []PassportTenant
	if err := json.Unmarshal(body, &tenants); err != nil {
		return nil, wireError(fmt.Errorf("解析租户列表失败: %w", err))
	}
	return tenants, nil
}

// SetActiveTenant 切换当前活动租户：持久化到 desktop.json，并更新内存配置(供对话内
// 模型选择器与下次新建/恢复会话使用)。仅在通行证会话下更新 baseURL。
func (a *App) SetActiveTenant(tenantID string) error {
	tenantID = strings.TrimSpace(tenantID)
	raw := loadRawConfig()
	raw.TenantID = tenantID
	saveRawConfig(raw)
	pc := passportConfig()
	a.mu.Lock()
	a.passportTenant = tenantID
	if a.config.Provider == "openai" && strings.HasPrefix(a.config.BaseURL, pc.BridgeBaseURL) {
		a.config.BaseURL = pc.BridgeBaseURL + tenantPathPrefix(tenantID) + "/v1"
	}
	a.mu.Unlock()
	return nil
}

// ActiveTenant 返回当前活动租户 id(可能为空 = 令牌自带租户)。
func (a *App) ActiveTenant() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.passportTenant
}

// SessionModels 返回当前通行证会话所选租户的平台模型，供对话内模型选择器使用；
// 未登录返回空。
func (a *App) SessionModels() ([]PassportModel, error) {
	if !a.tokens.LoggedIn() {
		return nil, nil
	}
	a.mu.Lock()
	tid := a.passportTenant
	a.mu.Unlock()
	return a.PassportModels(tid)
}

// PassportModels 经 Bridge 列指定租户的平台模型（tenantID 空 = 令牌自带租户）。
func (a *App) PassportModels(tenantID string) ([]PassportModel, error) {
	body, err := a.bridgeGet(tenantPathPrefix(tenantID) + "/v1/models")
	if err != nil {
		return nil, wireError(err)
	}
	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, wireError(fmt.Errorf("解析模型列表失败: %w", err))
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
		return PassportStatus{}, wireError(err)
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
		return PassportStatus{}, wireError(err)
	}
	return PassportStatus{LoggedIn: true, UserID: me.UserID, UserName: me.UserName,
		Name: me.Name, Nickname: me.Nickname, Avatar: me.Avatar, TenantID: me.TenantID}, nil
}

// bridgeGet 带登录令牌 GET Bridge 端点，返回响应体。
func (a *App) bridgeGet(path string) ([]byte, error) {
	tok, err := a.tokens.Token()
	if err != nil {
		return nil, wireError(err)
	}
	cfg := passportConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.BridgeBaseURL+path, nil)
	if err != nil {
		return nil, wireError(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := passportHTTP().Do(req)
	if err != nil {
		return nil, wireError(fmt.Errorf("访问中间服务失败: %w", err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, wireError(fmt.Errorf("中间服务返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
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
	// 选定租户 → /t/{tenantId}/v1，让 Bridge 按该租户计费/限额；空则走令牌自带租户。
	cfg.BaseURL = pc.BridgeBaseURL + tenantPathPrefix(req.TenantID) + "/v1"
	cfg.APIKey = ""
	cfg.AuthToken = ""
	cfg.TokenSource = a.tokens.Token
	// 服务端 401(令牌过期/被拒)时强制刷新一次令牌后重试。
	cfg.OnUnauthorized = a.tokens.ForceRefresh
	a.mu.Lock()
	a.passportTenant = strings.TrimSpace(req.TenantID)
	a.mu.Unlock()
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
