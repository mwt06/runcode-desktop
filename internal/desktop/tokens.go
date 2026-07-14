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
			m.onLoggedOut()
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
