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
//
// 硬约束：绝不返回 ("", nil) —— 快路径要求 access token 非空，否则落到刷新分支。
// 锁语义：显式加解锁而非 defer，因为不同阶段需要不同的持锁范围：
//   - 刷新网络调用（refreshGrant）必须持锁：Passport 的 refresh token 一次性使用
//     （轮换），并发调用者若都拿着同一个 refresh token 发起刷新，先到者成功、
//     后到者会收到 invalid_grant 并被误判为登出。持锁把刷新串行化：并发调用者
//     阻塞在 m.mu.Lock() 上，等前一个刷新完成后走快路径拿新 token，不会重复刷新。
//     不要为“优化并发”而在刷新期间释放锁。
//   - 回调（onLoggedOut）与磁盘落盘（persistTokens）必须在解锁之后调用：
//     前者避免 tokenManager.mu 与调用方（如 App.mu）之间形成锁序耦合，
//     后者是磁盘 I/O，不应占着内存锁等待文件系统，且与 Set() 的落锁方式一致。
func (m *tokenManager) Token() (string, error) {
	m.mu.Lock()
	if m.ts.AccessToken == "" && m.ts.RefreshToken == "" {
		m.mu.Unlock()
		return "", errNotLoggedIn
	}
	// 快路径要求 access token 非空（合同：绝不返回 ("", nil)）
	if m.ts.AccessToken != "" && time.Until(m.ts.Expiry) > refreshSkew {
		tok := m.ts.AccessToken
		m.mu.Unlock()
		return tok, nil
	}
	if m.ts.RefreshToken == "" {
		m.clearLocked()
		m.mu.Unlock()
		return "", errNotLoggedIn
	}
	// 持锁刷新：refresh token 一次性使用（轮换），串行化避免并发双刷
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	fresh, err := refreshGrant(ctx, m.hc, m.tokenURL, m.clientID, m.ts.RefreshToken)
	cancel()
	if err != nil {
		m.clearLocked()
		m.mu.Unlock()
		// 回调与事件发射不持锁（避免 tokenManager.mu → App.mu 锁序耦合）
		if m.onLoggedOut != nil {
			m.onLoggedOut()
		}
		return "", errors.Join(errNotLoggedIn, err)
	}
	m.ts = fresh
	m.mu.Unlock()
	persistTokens(fresh) // 磁盘 I/O 不持锁，与 Set() 一致
	return fresh.AccessToken, nil
}

// ForceRefresh 无视续期窗口立即用 refresh token 续期一次，用于服务端返回 401
// (令牌被拒)时的补救。持锁刷新(与 Token 一致的单飞语义)；回调/落盘在锁外。
// 无 refresh token 或续期失败则清空并通知登出。
func (m *tokenManager) ForceRefresh() {
	m.mu.Lock()
	if m.ts.RefreshToken == "" {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	fresh, err := refreshGrant(ctx, m.hc, m.tokenURL, m.clientID, m.ts.RefreshToken)
	cancel()
	if err != nil {
		m.clearLocked()
		m.mu.Unlock()
		if m.onLoggedOut != nil {
			m.onLoggedOut()
		}
		return
	}
	m.ts = fresh
	m.mu.Unlock()
	persistTokens(fresh)
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
