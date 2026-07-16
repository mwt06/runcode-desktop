package desktop

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/wt68/runcode/internal/webclient"
)

// WebProxy 返回联网工具(WebFetch/WebSearch)使用的代理地址(空 = 直连)。
func (a *App) WebProxy() string { return loadRawConfig().WebProxy }

// SetWebProxy 设置联网工具的代理并持久化，返回规范化后的地址(空 = 直连)。
// 只影响 WebFetch/WebSearch，不影响模型/通行证的请求——那些走各自的客户端。
// 工具的 HTTP 客户端在建会话时构造，故改动对**新建/恢复的会话**生效。
func (a *App) SetWebProxy(v string) (string, error) {
	norm, err := normalizeProxy(v)
	if err != nil {
		return "", err
	}
	raw := loadRawConfig()
	raw.WebProxy = norm
	saveRawConfig(raw)
	applyWebProxy(norm)
	return norm, nil
}

// applyWebProxy 把设置发布给联网工具。工具在构造 HTTP 客户端时从环境变量读取
// (webclient.ProxyEnvVar)，用环境变量当传递通道是为了不必给每个工具构造函数
// (tools.Builtins → websearch.New)都加一个配置参数。用的是 runcode 专属变量而非
// HTTPS_PROXY，所以这里的设置不会顺带改变模型 API 的出口。
func applyWebProxy(v string) {
	if strings.TrimSpace(v) == "" {
		_ = os.Unsetenv(webclient.ProxyEnvVar)
		return
	}
	_ = os.Setenv(webclient.ProxyEnvVar, v)
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
