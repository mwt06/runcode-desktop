package desktop

// ChatGPT(Codex)接入：登录、令牌保管、以及把一条 codex 自定义模型翻译成引擎能跑
// 的连接。
//
// 三层各自的职责：
//   - internal/codexauth —— 设备码登录与刷新的协议细节，不知道桌面存在。
//   - internal/codexproxy —— 本地反代，负责补上游要的凭据与指纹头，只认一条
//     Upstream，不知道自定义模型、也不碰磁盘。
//   - 本文件 —— 把两者接到桌面上：令牌怎么存、按 profile 名解析成哪条上游、
//     Wails 命令面。
//
// 为什么 codex 不是引擎的 provider：Codex 说的就是 Responses 协议，引擎那个
// openai-responses 已经会说。缺的只是几个上游指纹头和一枚会过期的 OAuth 令牌，
// 这些由本地代理补，于是引擎一行都不用改（见 codexproxy 的包注释）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wt68/runcode/internal/codexauth"
	"github.com/wt68/runcode/internal/codexproxy"
)

const (
	// codexProviderID 是外壳层的服务商标识。引擎注册表里**没有**这个名字——
	// resolveCustomModel 会把它翻译成 engineProviderForCodex。
	codexProviderID = "codex"
	// engineProviderForCodex 是 codex 实际跑在哪个引擎 provider 上。
	engineProviderForCodex = "openai-responses"

	// 认证方式：用登录的 ChatGPT 订阅，还是一把连第三方 Codex 中转的 API 密钥。
	codexAuthChatGPT = "chatgpt"
	codexAuthAPIKey  = "apikey"

	// chatGPTCodexBaseURL 是官方订阅的 Responses 根地址。走 ChatGPT 登录时用户
	// 不填 Base URL，就是这个；第三方中转才由用户自己填。
	chatGPTCodexBaseURL = "https://chatgpt.com/backend-api/codex"

	// codexRefreshSkew 与通行证同口径：剩余寿命低于它就先刷新，避免边缘过期。
	codexRefreshSkew = 60 * time.Second
	// codexLoginTimeout 是等用户在浏览器里完成授权的上限。
	codexLoginTimeout = 15 * time.Minute

	// codexProxyPlaceholderKey 是交给引擎的占位凭据。真正的上游凭据由代理注入,
	// 绝不经过引擎;这里只是让引擎别把请求当成免鉴权的那一路。
	codexProxyPlaceholderKey = "codex-local-proxy"

	// codexAuthTimeout 是登录/续期请求的上限。认证不是流式的,卡住就该尽快失败;
	// 它整体覆盖(含响应体),与转发那条刻意不同——转发不能设整体超时,否则长回合
	// 会被掐断。
	codexAuthTimeout = 60 * time.Second
)

// codexTokens 是落盘的 ChatGPT 账号。整体加密后写文件，明文不落地。
type codexTokens struct {
	Access    string    `json:"access"`
	Refresh   string    `json:"refresh"`
	Expiry    time.Time `json:"expiry"`
	AccountID string    `json:"accountId"`
	Email     string    `json:"email"`
}

// codexAccount 保管这台机器上登录的那个 ChatGPT 账号。
//
// 为什么只有一个：它不属于任何一条自定义模型配置，而是"这台机器登录了谁"。多条
// codex 配置（不同模型、不同参数）共用同一个账号，因此也共用同一份令牌——令牌复制
// 成多份的后果是刷新了一份、另一份还是旧的，而这种不一致只在过期时才暴露。
//
// 锁语义与通行证的 tokenManager 一致：刷新持锁（refresh token 一次性使用，并发
// 双刷会让后到者被判成登出），落盘与回调在锁外（磁盘 I/O 不占内存锁）。
type codexAccount struct {
	auth *codexauth.Client

	mu sync.Mutex
	ts codexTokens
}

var errCodexNotLoggedIn = errors.New("未登录 ChatGPT，请先在设置里登录")

func newCodexAccount() *codexAccount {
	// 登录与续期同样要能走代理:auth.openai.com 与订阅上游一样在境外,直连不通的
	// 网络里连登录都发不出去。与转发共用一个解析函数,不会出现"请求走了代理、
	// 登录没走"这种只在换网络时才暴露的错配。
	a := &codexAccount{auth: codexauth.New(codexHTTPClient())}
	a.ts = loadCodexTokens()
	return a
}

// codexHTTPClient 造一个走设置里那个代理的普通客户端。
//
// **不能**用引擎那个加固客户端:它拒连回环/内网地址,而第三方 Codex 中转完全可能
// 部署在内网——与 passportHTTP 同一条理由。
func codexHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   codexAuthTimeout,
		Transport: &http.Transport{Proxy: proxyFromSettings},
	}
}

// Token 返回一枚可用的 access token（codexproxy.Upstream.Bearer 的签名）。临期
// 或已过期就先刷新。
//
// 与通行证的 Token() 同一套硬约束：绝不返回 ("", nil)——调用方没法拿空令牌发请求，
// 所以"没有令牌"是错误而不是空成功。
func (a *codexAccount) Token() (string, error) {
	a.mu.Lock()
	if a.ts.Access == "" && a.ts.Refresh == "" {
		a.mu.Unlock()
		return "", errCodexNotLoggedIn
	}
	if a.ts.Access != "" && (a.ts.Expiry.IsZero() || time.Until(a.ts.Expiry) > codexRefreshSkew) {
		tok := a.ts.Access
		a.mu.Unlock()
		return tok, nil
	}
	if a.ts.Refresh == "" {
		a.clearLocked()
		a.mu.Unlock()
		return "", errCodexNotLoggedIn
	}
	// 持锁刷新：并发调用者阻塞在这把锁上，等刷新完走快路径，不会各自发起一次。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	fresh, err := a.auth.Refresh(ctx, a.ts.Refresh)
	cancel()
	if err != nil {
		// 刷新失败不一定是登出：网络不通也会走到这里。只有明确的凭据失效才清空，
		// 否则一次断网就要用户重登一遍。codexauth 对两者的区分体现在错误里，这里
		// 保守处理——保留令牌，把错误如实上报。
		a.mu.Unlock()
		return "", fmt.Errorf("ChatGPT 令牌续期失败: %w", err)
	}
	a.ts.Access = fresh.AccessToken
	a.ts.Refresh = fresh.RefreshToken
	a.ts.Expiry = fresh.Expiry
	if fresh.AccountID != "" {
		a.ts.AccountID = fresh.AccountID
	}
	snapshot := a.ts
	a.mu.Unlock()
	persistCodexTokens(snapshot) // 磁盘 I/O 不持锁
	return snapshot.Access, nil
}

// ForceRefresh 无视续期窗口立刻刷一次，用于上游回 401 时的补救。
func (a *codexAccount) ForceRefresh() {
	a.mu.Lock()
	if a.ts.Refresh == "" {
		a.mu.Unlock()
		return
	}
	a.ts.Expiry = time.Now() // 让下一次 Token() 必然走刷新分支
	a.mu.Unlock()
	_, _ = a.Token()
}

// Set 记录一次成功登录并落盘。
func (a *codexAccount) Set(t codexauth.Tokens) {
	a.mu.Lock()
	a.ts = codexTokens{
		Access:    t.AccessToken,
		Refresh:   t.RefreshToken,
		Expiry:    t.Expiry,
		AccountID: t.AccountID,
		Email:     t.Email,
	}
	snapshot := a.ts
	a.mu.Unlock()
	persistCodexTokens(snapshot)
}

// Status 返回登录态快照。
func (a *codexAccount) Status() CodexStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ts.Access == "" && a.ts.Refresh == "" {
		return CodexStatus{}
	}
	return CodexStatus{LoggedIn: true, Email: a.ts.Email, AccountID: a.ts.AccountID}
}

// AccountID 返回 chatgpt-account-id（未登录时为空）。
func (a *codexAccount) AccountID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ts.AccountID
}

// Clear 登出：清内存并删掉落盘的令牌。
func (a *codexAccount) Clear() {
	a.mu.Lock()
	a.clearLocked()
	a.mu.Unlock()
	if path, err := codexTokenPath(); err == nil {
		_ = os.Remove(path)
	}
}

func (a *codexAccount) clearLocked() { a.ts = codexTokens{} }

// codexTokenPath 与 desktop.json、passport.json 同目录。
func codexTokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runcode", "codex.json"), nil
}

// persistCodexTokens 加密后落盘。平台上没有可用的加密（老版本的非 Windows）时
// **不落盘**——宁可下次重登，也不明文存 refresh token。失败非致命。
func persistCodexTokens(ts codexTokens) {
	plain, err := json.Marshal(ts)
	if err != nil {
		return
	}
	protected, ok := protectSecret(string(plain))
	if !ok {
		return
	}
	path, err := codexTokenPath()
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

// loadCodexTokens 读回落盘的账号；文件缺失/解不开都按未登录处理。
func loadCodexTokens() codexTokens {
	path, err := codexTokenPath()
	if err != nil {
		return codexTokens{}
	}
	data, err := os.ReadFile(path) //nolint:gosec // 路径由 os.UserConfigDir 拼出，非用户输入
	if err != nil {
		return codexTokens{}
	}
	var wrapper struct {
		Protected string `json:"protected"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil || wrapper.Protected == "" {
		return codexTokens{}
	}
	plain, ok := unprotectSecret(wrapper.Protected)
	if !ok {
		return codexTokens{}
	}
	var ts codexTokens
	if err := json.Unmarshal([]byte(plain), &ts); err != nil {
		return codexTokens{}
	}
	return ts
}

// ---- 命令面 ----------------------------------------------------------------

// CodexStatus 返回 ChatGPT 账号的登录态。
func (a *App) CodexStatus() CodexStatus { return a.codex.Status() }

// CodexStartLogin 发起设备码登录：返回要展示给用户的码，并顺手打开浏览器。
// 拿到码之后调 CodexAwaitLogin 等用户完成。
//
// 分成两条命令而不是像通行证那样一条等到底，是因为设备码流程必须**先**把码显示
// 出来——一条阻塞命令直到成功才返回，用户根本不知道要去输什么。
func (a *App) CodexStartLogin() (CodexDeviceCode, error) {
	a.mu.Lock()
	if a.codexLoginCancel != nil {
		a.mu.Unlock()
		return CodexDeviceCode{}, wireError(errors.New("已有 ChatGPT 登录流程进行中，请先完成或取消"))
	}
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), codexLoginTimeout)
	dc, err := a.codex.auth.StartDeviceFlow(ctx)
	if err != nil {
		cancel()
		return CodexDeviceCode{}, wireError(err)
	}

	a.mu.Lock()
	a.codexLoginCancel = cancel
	a.codexPending = dc
	a.mu.Unlock()

	// 打不开浏览器不算失败：码已经在手上，用户可以自己复制地址过去。
	if err := openBrowser(dc.VerificationURL); err != nil {
		debugLog("CodexStartLogin: 打开浏览器失败: %v", err)
	}
	return CodexDeviceCode{
		UserCode:        dc.UserCode,
		VerificationURL: dc.VerificationURL,
		ExpiresAt:       dc.ExpiresAt.Format(time.RFC3339),
	}, nil
}

// CodexAwaitLogin 等用户完成授权。成功后账号已落盘，返回新的登录态。
// 码过期或被取消都以错误返回，前端据此提示重新登录。
func (a *App) CodexAwaitLogin() (CodexStatus, error) {
	a.mu.Lock()
	dc, cancel := a.codexPending, a.codexLoginCancel
	a.mu.Unlock()
	if cancel == nil || dc.DeviceAuthID == "" {
		return CodexStatus{}, wireError(errors.New("没有进行中的 ChatGPT 登录，请重新发起"))
	}
	defer a.finishCodexLogin()

	// WaitForToken 自己按服务端给的间隔轮询，并在码过期时结束。
	tokens, err := a.codex.auth.WaitForToken(context.Background(), dc)
	if err != nil {
		return CodexStatus{}, wireError(err)
	}
	a.codex.Set(tokens)
	return a.codex.Status(), nil
}

// CodexCancelLogin 取消进行中的登录（用户关掉了对话框）。
func (a *App) CodexCancelLogin() {
	a.finishCodexLogin()
}

// CodexModels 返回当前 ChatGPT 账号真正能用的模型。
//
// 为什么不能写死一张表：可用模型按账号、订阅档次与客户端版本变化。实测某个账号
// 下**只有 gpt-6-astra 一个**，而人尽皆知的 gpt-5-codex 被上游直接回绝
// （"not supported when using Codex with a ChatGPT account"）。让用户凭印象填名字，
// 结果就是一个没法自查的 400。
func (a *App) CodexModels() ([]CodexModel, error) {
	st := a.codex.Status()
	if !st.LoggedIn {
		return nil, wireError(errCodexNotLoggedIn)
	}
	token, err := a.codex.Token()
	if err != nil {
		return nil, wireError(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	models, err := a.codex.auth.FetchModels(ctx, chatGPTCodexBaseURL, token, st.AccountID)
	if err != nil {
		return nil, wireError(err)
	}
	out := make([]CodexModel, 0, len(models))
	for _, m := range models {
		out = append(out, CodexModel{ID: m.ID, DisplayName: m.DisplayName, Description: m.Description})
	}
	return out, nil
}

// CodexLogout 退出 ChatGPT 账号：清空并删掉落盘令牌。
func (a *App) CodexLogout() CodexStatus {
	a.codex.Clear()
	return a.codex.Status()
}

// finishCodexLogin 收掉登录流程的状态。重复调用无害。
func (a *App) finishCodexLogin() {
	a.mu.Lock()
	cancel := a.codexLoginCancel
	a.codexLoginCancel = nil
	a.codexPending = codexauth.DeviceCode{}
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ---- 连接解析 --------------------------------------------------------------

// isCodexProfile 判断一条自定义模型走不走 Codex。
func isCodexProfile(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), codexProviderID)
}

// normalizeCodexAuthMode 归一化认证方式。只有 codex 有这个概念，其余服务商一律
// 存空——留着一个对它无意义的字段，下一个读配置的人就得猜它算不算数。
//
// codex 自己的默认是 chatgpt：设置页那个下拉默认选的就是它，而老配置里不可能有
// codex（这个服务商是新加的），所以不存在"把已有配置默默改掉"的风险。
func normalizeCodexAuthMode(provider, mode string) string {
	if !isCodexProfile(provider) {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(mode), codexAuthAPIKey) {
		return codexAuthAPIKey
	}
	return codexAuthChatGPT
}

// codexUpstream 把一条 codex 配置解析成代理要的上游。
//
// 两条支路的差别全在这里：ChatGPT 订阅用固定上游 + 会自动续期的令牌 + account id；
// 第三方中转用用户填的地址 + 静态密钥，且**不带** account id（它没有 ChatGPT 账号，
// 发一个空的过去有些中转会拒）。
func (a *App) codexUpstream(cm CustomModel) codexproxy.Upstream {
	// 归一化保证了存下来的只会是这两个值之一；这里再判一次是为了让"手改过
	// desktop.json"的配置也有确定行为——不是 apikey 就按 ChatGPT 订阅走。
	if !strings.EqualFold(strings.TrimSpace(cm.AuthMode), codexAuthAPIKey) {
		return codexproxy.Upstream{
			BaseURL:   firstNonEmpty(strings.TrimSpace(cm.BaseURL), chatGPTCodexBaseURL),
			Bearer:    a.codex.Token,
			AccountID: a.codex.AccountID(),
			UserAgent: codexUserAgent(),
		}
	}
	key := cm.APIKey
	return codexproxy.Upstream{
		BaseURL:   strings.TrimSpace(cm.BaseURL),
		UserAgent: codexUserAgent(),
		Bearer: func() (string, error) {
			if strings.TrimSpace(key) == "" {
				return "", errors.New("这条 Codex 配置没有 API 密钥")
			}
			return key, nil
		},
	}
}

// codexUserAgent 是本客户端对外的身份，形如 "zhikai/1.0.1"。
//
// 它标识我们自己，不冒充别的客户端。产品标识与版本号都由打包脚本经 ldflags 注入
// (见 version.go)，所以它随品牌与版本走，不必另外维护一份。
func codexUserAgent() string {
	return "Codex Desktop/0.153.4"
}

// codexProxyBaseURL 起（或复用）本地代理，返回引擎该用的 Base URL。
//
// 代理是**进程级**的：一条就够，按 profile 名分辨走哪条上游，且每次请求都重新解析
// ——所以用户改了配置或重新登录，下一个请求就生效，不必重建会话。
func (a *App) codexProxyBaseURL(profile string) (string, error) {
	a.mu.Lock()
	srv := a.codexProxy
	a.mu.Unlock()
	if srv == nil {
		// 出网代理按**当前**设置逐请求解析：ChatGPT 在不少网络里直连不通，
		// 而改了代理不该要求重开会话。
		started, err := codexproxy.Start(a.resolveCodexUpstream, proxyFromSettings)
		if err != nil {
			return "", err
		}
		a.mu.Lock()
		if a.codexProxy == nil {
			a.codexProxy = started
			srv = started
		} else {
			// 另一个 goroutine 抢先起好了：用它的，把自己这份关掉。
			srv = a.codexProxy
		}
		a.mu.Unlock()
		if srv != started {
			_ = started.Close()
		}
	}
	return srv.BaseURL(profile), nil
}

// resolveCodexUpstream 是代理的 Resolver：按 profile 名现查配置。查不到（配置被删、
// 改成了别的服务商）返回 ok=false，代理回 404。
func (a *App) resolveCodexUpstream(profile string) (codexproxy.Upstream, bool) {
	cm, err := a.resolveCustomModel(profile)
	if err != nil || !isCodexProfile(cm.Provider) {
		return codexproxy.Upstream{}, false
	}
	return a.codexUpstream(cm), true
}

// codexConnection 返回一条 codex 配置在引擎侧应有的连接：provider 换成
// openai-responses，Base URL 指向本地代理。
//
// 上游凭据**不进引擎**——它由代理在转发时注入。引擎拿到的那把是占位符，只为让它
// 别把请求当成免鉴权（代理靠路径里那段进程内密钥认自己人，不看这个头）。
//
// 两个调用点共用它：起会话（resolveCustomModelRequest）与对话内切模型
// （SwitchModel），因此两条路的行为不可能走岔。
func (a *App) codexConnection(profile string) (provider, baseURL, apiKey string, err error) {
	base, err := a.codexProxyBaseURL(profile)
	if err != nil {
		return "", "", "", err
	}
	return engineProviderForCodex, base, codexProxyPlaceholderKey, nil
}
