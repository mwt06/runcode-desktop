package desktop

// 已被内置工具取代的 MCP 服务器:下架、清理、并且**不再连接**。
//
// # 为什么需要这件事
//
// OA 曾经是一台 MCP 服务器(基座市场里的 oa 条目,一键装进 config.toml)。现在它是
// 内置工具(oa_*,见 oa.go)。两者并存的后果不是"多一份重复能力",而是**一条绕过闸门
// 的通路**:内置那批每次调用前都要过"当前会话是不是本地模型"的闸,MCP 那批不过——
// 它由引擎统一装配,宿主没有地方插进这个判断。模型看到 mcp__oa__my_todo 就调,OA 数据
// 于是进了一条跑在云端模型上的会话。
//
// # 三道一起做,因为它们各自都可能失灵
//
//  1. **基座下架** —— 市场清单里删掉 oa 条目(bridge 的 bridge.mcp-market)。这是正本
//     清源,但只对"已经更新了 ConfigMap 的部署"有效。
//  2. **本地清理** —— 启动时把已装的那条从 config.toml 里删掉。但配置可能只读、可能
//     写失败,而且它发生在进程启动后。
//  3. **装配时过滤** —— loadDesktopMCP 里跳过退役条目。这条是兜底:**即便前两道都没
//     生效,退役的服务器也连不上**。这是唯一一道不依赖网络、不依赖写盘的闸。
//
// # 识别口径为什么带上 passport 这一条
//
// 只按名字删太粗:MCPServerInput.Passport 是用户可勾的,名叫 oa 的条目未必是基座装的。
// 但反过来"名叫 oa **且**勾了 passport"恰好就是**能拿到用户令牌去调平台 OA 服务**的
// 那个集合——没勾 passport 的条目拿不到令牌(见 mcppassport.go),打过去只会 401,
// 它连不连得上都与保密数据无关,不该被我们删掉。
//
// 所以这条规则的含义是精确的:凡是能以登录用户身份调到 OA 的 MCP 条目,一律退役。

import (
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/settings"
)

// retiredMCPServers 是已被内置工具取代的市场条目 id → 取代它的东西(供日志与说明)。
//
// 键就是市场条目的 id,也是它在 config.toml 里的条目名(见 McpServerEntry.id 的约定)。
var retiredMCPServers = map[string]string{
	"oa": "OA 办公工具已内置（oa_* 系列），不再经 MCP",
}

// isRetiredMCPServer 判断一条本地 MCP 配置是不是已退役的那种。
//
// 纯函数:这条规则要被三处共用(装配过滤、启动清理、市场列表),分叉的那一处就是漏洞。
func isRetiredMCPServer(name string, s settings.MCPServerConfig) bool {
	if _, ok := retiredMCPServers[strings.TrimSpace(name)]; !ok {
		return false
	}
	// 见文件头:只有"能拿到用户令牌"的那些才退役。
	return mcpPassportEnabled(s)
}

// filterRetiredMCPServers 从一份 MCP 配置里剔除退役条目,返回过滤后的配置与被剔除的
// 名字。原 map 不被修改——调用方拿到的是 settings.Load 的结果,那是它自己的东西。
func filterRetiredMCPServers(cfg settings.MCPConfig) (settings.MCPConfig, []string) {
	if len(cfg.Servers) == 0 {
		return cfg, nil
	}
	var retired []string
	kept := make(map[string]settings.MCPServerConfig, len(cfg.Servers))
	for name, s := range cfg.Servers {
		if isRetiredMCPServer(name, s) {
			retired = append(retired, name)
			continue
		}
		kept[name] = s
	}
	if len(retired) == 0 {
		return cfg, nil
	}
	cfg.Servers = kept
	return cfg, retired
}

// retireMCPServers 把已退役的条目从 config.toml 里删掉。
//
// 启动时跑一次。**删除而不是停用**:停用的条目在 MCP 页面上还是一个开关,用户随手打开
// 就把绕过闸门的那条路又接上了,而他不会知道这一点。能力本身没有丢——同样的 15 项现在
// 是内置工具,不需要用户做任何事。
//
// 尽力而为:配置只读、写失败,都不影响正确性——装配时的过滤(loadDesktopMCP)已经保证
// 退役的服务器连不上,这里只是让界面上不再留着一条死条目。
func (a *App) retireMCPServers() {
	mcpMu.Lock()
	defer mcpMu.Unlock()
	servers, err := a.loadMCPServers()
	if err != nil || len(servers) == 0 {
		return
	}
	changed := false
	for name, s := range servers {
		if !isRetiredMCPServer(name, s) {
			continue
		}
		debugLog("mcp: 下架已内置的服务器 %q（%s）", name, retiredMCPServers[name])
		delete(servers, name)
		changed = true
	}
	if !changed {
		return
	}
	if err := a.writeMCPServers(servers); err != nil {
		debugLog("mcp: 下架写盘失败（装配时仍会跳过它）: %v", err)
	}
}

// dropRetiredMarketEntries 从基座市场清单里去掉已退役的条目。
//
// 客户端不等基座更新 ConfigMap:哪些能力已经内置是**客户端自己知道**的事实,而市场清单
// 是一份可能落后于客户端的远端配置。不过滤的话,一个没更新的部署会继续把 OA 摆在市场里
// 让人一键装回来——装回来的正是那条绕过闸门的路。
func dropRetiredMarketEntries(entries []McpMarketEntry) []McpMarketEntry {
	out := entries[:0:0]
	for _, e := range entries {
		if _, retired := retiredMCPServers[strings.TrimSpace(e.ID)]; retired {
			continue
		}
		out = append(out, e)
	}
	return out
}
