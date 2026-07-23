package desktop

import (
	"fmt"
	"net/url"
	"strings"
)

// WebProxy 返回联网工具(WebFetch/WebSearch)使用的代理地址(空 = 直连)。
func (a *App) WebProxy() string { return loadRawConfig().WebProxy }

// SetWebProxy 设置联网工具的代理并持久化，返回规范化后的地址(空 = 直连)。
// 只影响 WebFetch/WebSearch，不影响模型/通行证的请求——那些走各自的客户端。
// 工具的 HTTP 客户端在建会话时构造，故改动对**新建/恢复的会话**生效。
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
