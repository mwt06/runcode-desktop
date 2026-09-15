package desktop

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/codexauth"
)

// codex is a shell-level provider, not an engine one: it must pass validation
// here or saving the profile fails, yet never reach the engine under that name.
func TestCodexProviderIsAcceptedButNotAnEngineProvider(t *testing.T) {
	if !supportedCustomModelProvider(codexProviderID) {
		t.Fatal("codex was rejected as a provider; profiles could not be saved")
	}
	if engineProviderForCodex != "openai-responses" {
		t.Fatalf("codex maps to %q, want the engine's Responses provider", engineProviderForCodex)
	}
	if !isCodexProfile("Codex") || !isCodexProfile(" codex ") {
		t.Fatal("provider matching must tolerate case and surrounding space")
	}
	if isCodexProfile("openai") {
		t.Fatal("a non-codex provider matched")
	}
}

// The auth mode is only meaningful for codex; leaving it set on other providers
// would leave the next reader guessing whether it counts.
func TestNormalizeCodexAuthMode(t *testing.T) {
	for _, tc := range []struct{ provider, in, want string }{
		{"codex", "", codexAuthChatGPT},         // default: the subscription
		{"codex", "chatgpt", codexAuthChatGPT},  //
		{"codex", "apikey", codexAuthAPIKey},    // third-party relay
		{"codex", "APIKey", codexAuthAPIKey},    // case-insensitive
		{"codex", "nonsense", codexAuthChatGPT}, // unknown falls back to the default
		{"openai", "chatgpt", ""},               // not a codex profile
		{"anthropic", "apikey", ""},
	} {
		if got := normalizeCodexAuthMode(tc.provider, tc.in); got != tc.want {
			t.Errorf("normalizeCodexAuthMode(%q, %q) = %q, want %q", tc.provider, tc.in, got, tc.want)
		}
	}
}

// The ChatGPT-subscription branch uses the fixed official upstream and carries
// the account id; a third-party relay uses the user's own URL and must not send
// an account id it doesn't have.
func TestCodexUpstreamBranches(t *testing.T) {
	// 必须隔离：Set 会把令牌落到 %AppData%/runcode/codex.json，Clear 会把它删掉
	// ——不隔离就是拿开发者本人的 ChatGPT 登录做测试数据(真发生过)。
	isolateConfigDir(t)
	app := New(&recordingSink{})
	app.codex.Set(codexauth.Tokens{
		AccessToken:  "AT",
		RefreshToken: "RT",
		Expiry:       time.Now().Add(time.Hour),
		AccountID:    "acct-1",
		Email:        "u@example.com",
	})

	sub := app.codexUpstream(CustomModel{Provider: "codex", AuthMode: codexAuthChatGPT})
	if sub.BaseURL != chatGPTCodexBaseURL {
		t.Fatalf("subscription base URL = %q, want the official Codex endpoint", sub.BaseURL)
	}
	if sub.AccountID != "acct-1" {
		t.Fatalf("account id = %q, want the logged-in account's", sub.AccountID)
	}
	if tok, err := sub.Bearer(); err != nil || tok != "AT" {
		t.Fatalf("bearer = (%q, %v), want the account's live token", tok, err)
	}

	relay := app.codexUpstream(CustomModel{
		Provider: "codex",
		AuthMode: codexAuthAPIKey,
		BaseURL:  "https://relay.example/v1",
		APIKey:   "sk-relay",
	})
	if relay.BaseURL != "https://relay.example/v1" {
		t.Fatalf("relay base URL = %q, want the user's own", relay.BaseURL)
	}
	if relay.AccountID != "" {
		t.Fatal("a relay upstream carried a ChatGPT account id")
	}
	if tok, err := relay.Bearer(); err != nil || tok != "sk-relay" {
		t.Fatalf("relay bearer = (%q, %v), want the profile's API key", tok, err)
	}
}

// A relay profile saved without a key must fail at request time with a reason,
// not send an empty bearer upstream.
func TestCodexRelayWithoutKeyFails(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	up := app.codexUpstream(CustomModel{Provider: "codex", AuthMode: codexAuthAPIKey, BaseURL: "https://r.example"})
	if _, err := up.Bearer(); err == nil {
		t.Fatal("a keyless relay profile produced a bearer")
	}
}

// Not being logged in is an error, never an empty success — the caller cannot
// send an empty bearer, so "no token" must not look like one.
func TestCodexTokenWithoutLoginErrors(t *testing.T) {
	isolateConfigDir(t)
	acct := &codexAccount{auth: codexauth.New(nil)}
	tok, err := acct.Token()
	if err == nil {
		t.Fatal("Token() succeeded with no account")
	}
	if tok != "" {
		t.Fatalf("token = %q, want empty alongside the error", tok)
	}
	if acct.Status().LoggedIn {
		t.Fatal("status reported logged in with no account")
	}
}

// The engine never receives the real upstream credential: the proxy injects it.
// A regression here would leak a ChatGPT token into engine-side config.
func TestCodexConnectionHidesRealCredential(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	t.Cleanup(func() {
		app.mu.Lock()
		srv := app.codexProxy
		app.mu.Unlock()
		if srv != nil {
			_ = srv.Close()
		}
	})
	app.codex.Set(codexauth.Tokens{AccessToken: "SECRET-AT", RefreshToken: "RT", Expiry: time.Now().Add(time.Hour)})

	provider, baseURL, apiKey, err := app.codexConnection("my-codex")
	if err != nil {
		t.Fatalf("codexConnection: %v", err)
	}
	if provider != engineProviderForCodex {
		t.Fatalf("provider = %q, want the engine's Responses provider", provider)
	}
	if !strings.HasPrefix(baseURL, "http://127.0.0.1:") {
		t.Fatalf("base URL = %q, want the loopback proxy", baseURL)
	}
	if apiKey == "SECRET-AT" || strings.Contains(baseURL, "SECRET-AT") {
		t.Fatal("the real upstream token reached the engine-side connection")
	}
	if apiKey != codexProxyPlaceholderKey {
		t.Fatalf("api key = %q, want the placeholder", apiKey)
	}
}

// One proxy per process: a second profile reuses it rather than leaking
// listeners, and each profile still addresses its own upstream.
func TestCodexProxyIsReusedAcrossProfiles(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	t.Cleanup(func() {
		app.mu.Lock()
		srv := app.codexProxy
		app.mu.Unlock()
		if srv != nil {
			_ = srv.Close()
		}
	})

	first, err := app.codexProxyBaseURL("alpha")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := app.codexProxyBaseURL("beta")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	// 用 url.Parse 取 host，不做字符串手术：原先是
	// rest[:strings.Index(rest, "/")]，而 Index 找不到时返回 -1，rest[:-1] 会 panic
	// ——断言没跑到就先崩了，失败信息还指向切片越界而不是这条测试真正在测的东西。
	hostOf := func(t *testing.T, raw string) string {
		t.Helper()
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("解析代理地址 %q: %v", raw, err)
		}
		return u.Host
	}
	if hostOf(t, first) != hostOf(t, second) {
		t.Fatalf("profiles got different proxies (%s vs %s); one per process expected", hostOf(t, first), hostOf(t, second))
	}
	if first == second {
		t.Fatal("two profiles produced the same URL; each must address its own upstream")
	}
}

// The resolver is what the proxy consults per request. A profile that is not a
// codex profile must not resolve, or a deleted/retyped profile would keep
// serving the previous upstream.
func TestResolveCodexUpstreamRejectsNonCodexProfiles(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	if _, ok := app.resolveCodexUpstream("does-not-exist"); ok {
		t.Fatal("an unknown profile resolved to an upstream")
	}
}

// 登录态必须活过重启：令牌加密落盘，下次启动的 App 直接是登录状态。
// 没有这条的时候，"登录一次能用、重开就要重登"这种问题只能靠用户报。
func TestCodexLoginSurvivesRestart(t *testing.T) {
	dir := isolateConfigDir(t)

	first := New(&recordingSink{})
	// 哨兵值刻意带 '-'：落盘的是 DPAPI 密文的 base64，而 base64 字母表里没有 '-'，
	// 所以"文件里出现哨兵"就一定是明文泄漏，不可能是密文里碰巧相邻的字符。
	//
	// 原先用的是 "RT"/"AT" 这样的两字母串，结果是**随机误报**：base64 里 R 和 T
	// 相邻的概率不低，测试于是时红时绿，而报错信息里打出来的那份内容本身是正确
	// 加密过的——看上去像"加密失效了"，实际是断言写错了。
	const refreshSentinel = "refresh-token-sentinel-must-not-appear"
	const emailSentinel = "user-sentinel@example.com"
	first.codex.Set(codexauth.Tokens{
		AccessToken:  "access-token-sentinel",
		RefreshToken: refreshSentinel,
		Expiry:       time.Now().Add(time.Hour),
		AccountID:    "acct-7",
		Email:        emailSentinel,
	})

	path := filepath.Join(dir, "runcode", "codex.json")

	// 系统凭据库不一定可用，而**不可用是一条正常分支，不是环境故障**：CI 的 Linux
	// runner 没有 Secret Service 的 D-Bus 会话，macOS runner 没有图形会话去解锁钥匙串。
	// 这时 protectSecret 报 ok=false，persistCodexTokens 一个字节都不写——契约是
	// "宁可下次重登，也不明文存 refresh token"。
	//
	// 所以这一支照样断言，而不是 t.Skip：恰恰是在不落盘的时候，最该守住的就是
	// "磁盘上什么都没有"。探针调的就是生产路径那同一个函数，上面 Set 已经调过一次，
	// 不会多出副作用。
	if _, ok := protectSecret("probe"); !ok {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("没有可用的系统凭据库，却还是写出了 %s（stat err=%v）", path, err)
		}
		if New(&recordingSink{}).codex.Status().LoggedIn {
			t.Fatal("没落盘，重启后却报已登录")
		}
		return
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("登录后没有落盘: %v", err)
	}
	// 明文绝不能出现在文件里:refresh token 是长期凭据。
	if bytes.Contains(raw, []byte(refreshSentinel)) || bytes.Contains(raw, []byte(emailSentinel)) {
		t.Fatalf("令牌以明文落盘了: %s", raw)
	}

	// "重启"：另起一个 App，读的是同一份磁盘状态。
	second := New(&recordingSink{})
	st := second.codex.Status()
	if !st.LoggedIn {
		t.Fatal("重启后登录态丢了")
	}
	if st.Email != emailSentinel || st.AccountID != "acct-7" {
		t.Fatalf("重启后账号信息 = %+v，与登录时不一致", st)
	}
	if tok, err := second.codex.Token(); err != nil || tok != "access-token-sentinel" {
		t.Fatalf("重启后取令牌 = (%q, %v)，want 原样可用", tok, err)
	}

	// 退出登录要把文件删掉，不能只清内存——否则下次启动又"自己登录上了"。
	second.codex.Clear()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("退出登录后文件仍在: %v", err)
	}
	if New(&recordingSink{}).codex.Status().LoggedIn {
		t.Fatal("退出登录后重启又变成已登录")
	}
}
