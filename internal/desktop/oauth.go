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
