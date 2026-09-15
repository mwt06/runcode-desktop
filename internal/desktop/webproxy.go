package desktop

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// WebProxy 返回联网工具使用的代理地址(空 = 直连)。覆盖面见 SetWebProxy。
func (a *App) WebProxy() string { return loadRawConfig().WebProxy }

// SetWebProxy 设置联网工具的代理并持久化，返回规范化后的地址(空 = 直连)。
// 不影响模型/通行证的请求——那些走各自的客户端。工具的 HTTP 客户端在建会话时
// 构造，故改动对**新建/恢复的会话**生效。
//
// 覆盖的是**引擎内置的**那两条联网工具：WebFetch，以及内置的 DuckDuckGo 版
// WebSearch。它们共用引擎按 engine.Config.WebProxy 建的那一个客户端。
//
// 通行证会话的联网搜索**不在其列**：那时 WebSearch 被换成走 Bridge 的平台搜索
// (见 websearch.go)，它自建普通客户端。这是刻意的——Bridge 常部署在内网，把它
// 塞进用户填的公网代理只会连不上。
func (a *App) SetWebProxy(v string) (string, error) {
	norm, err := normalizeProxy(v)
	if err != nil {
		return "", wireError(err)
	}
	// 只持久化，不再发布进程环境变量：建会话时 buildConfig/openSessionHeld 把
	// 持久化值注入 engine.Config.WebProxy（按会话隔离）；正在运行的会话保持原
	// 代理，下个新建/恢复的会话采用新值。
	if err := updateRawConfig(func(raw *StartSessionRequest) error {
		raw.WebProxy = norm
		return nil
	}); err != nil {
		return "", wireError(err)
	}
	return norm, nil
}

// normalizeProxy 校验并规范化用户填的代理地址。允许省略协议(Clash/v2ray 这类
// 客户端界面上常只显示 127.0.0.1:7890)，此时按 http 处理。
func normalizeProxy(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if !strings.Contains(v, "://") {
		v = "http://" + v
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("代理地址无效(示例 http://127.0.0.1:7890)")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", fmt.Errorf("不支持的代理协议 %q(支持 http/https/socks5)", u.Scheme)
	}
	return u.String(), nil
}

// proxyFromSettings 是按**当前**设置解析出的出网代理（nil = 直连），签名即
// http.Transport.Proxy。
//
// 每次请求都重读设置，所以改了代理下一个请求就生效——不必重建客户端，更不必
// 重开会话。这与联网工具那条不同（那条的客户端在建会话时构造，故"生效于下个
// 会话"），差别源于两者的客户端寿命，不是有意的不一致。
//
// 用途是 ChatGPT(Codex)：登录端点与订阅上游都在境外，直连不通的网络里没有代理
// 就完全用不了。地址解析不出来时按直连处理而不是报错——代理填错不该让请求变成
// 一个看不懂的失败，让它照常直连、失败在真正的原因上。
func proxyFromSettings(*http.Request) (*url.URL, error) {
	raw := strings.TrimSpace(loadRawConfig().WebProxy)
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, nil
	}
	return u, nil
}
