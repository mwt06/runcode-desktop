package desktop

import (
	"os"
	"strings"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"

	"github.com/wt68/runcode/internal/websearchtool"
)

// 联网搜索的后端选择。
//
// 登录了通行证的会话用平台自己的搜索(经 Bridge 调 AI.Core 的 /v1/websearch，带用户
// 令牌，按租户计费限额)；没登录通行证的连接——自填端点、自定义模型——保留引擎内置
// 的 DuckDuckGo 抓页搜索。理由是这条工具要的两样东西(Bridge 地址与用户令牌)只有
// 通行证连接才有，装一条注定每次都失败的工具比没有更糟。
//
// 替换经 engine.Options.WebSearchTool 走，不是 ExtraTools：会话内工具名唯一，同名
// 工具只能换不能加；而且 ExtraTools 只进主会话，子代理就会搜到另一个网上去。

// usingPassport 判定一条会话配置走的是不是通行证/Bridge 连接。
//
// 判据是 TokenSource：只有通行证那条路会装它(applyPassport，以及 SwitchModel 的
// 平台模型分支)，自填端点与自定义模型那条路一定把它清成 nil——那是刻意的，登录
// 凭据不能流向第三方端点。所以"有令牌源"与"连的是 Bridge"在本外壳里是一回事。
func usingPassport(cfg *engine.Config) bool {
	return cfg != nil && cfg.TokenSource != nil
}

// webSearchEndpoint 由会话的 Bridge 基地址推出搜索端点。BaseURL 形如
// <bridge>[/t/<租户>]/v1，所以选定的租户前缀自动跟着——联网搜索与对话记在同一个
// 租户名下，不必在这里再解析一遍租户。
func webSearchEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return ""
	}
	return base + "/websearch"
}

// passportWebSearch 造这条会话要用的 WebSearch 工具；非通行证连接返回 nil，引擎
// 保留内置的那条(见引擎 build.go 的 replaceWebSearch)。
//
// 搜索模型可用 RUNCODE_WEBSEARCH_MODEL 覆盖，留空由 websearchtool 填默认值——
// 默认值只有那一处，别在这里再写一遍。
//
// HTTP 客户端交给 websearchtool 自己建(普通带超时客户端)：**不能**用引擎那个加固
// 客户端，它拒连回环/内网地址，而 Bridge 常部署在内网——与 passportHTTP 同一条
// 理由。
func passportWebSearch(cfg *engine.Config) tool.Tool {
	if !usingPassport(cfg) {
		return nil
	}
	return websearchtool.New(websearchtool.Config{
		Endpoint:       webSearchEndpoint(cfg.BaseURL),
		Model:          os.Getenv("RUNCODE_WEBSEARCH_MODEL"),
		Token:          cfg.TokenSource,
		OnUnauthorized: cfg.OnUnauthorized,
	})
}
