package desktop

import (
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/settings"
)

func passportServer(url string) settings.MCPServerConfig {
	return withMCPPassport(settings.MCPServerConfig{Transport: "http", URL: url}, true)
}

func plainServer(url string) settings.MCPServerConfig {
	return settings.MCPServerConfig{Transport: "http", URL: url}
}

// 识别口径：名字对上**且**能拿到用户令牌，才算退役。
//
// 上半条防漏（基座装的那条必须退役），下半条防误伤：没勾 passport 的条目拿不到令牌，
// 打到平台 OA 服务只会 401，它是用户自己的东西，与保密数据无关，不该被我们删掉。
func TestRetiredMCPServerIdentification(t *testing.T) {
	cases := []struct {
		name   string
		server settings.MCPServerConfig
		want   bool
	}{
		{"oa", passportServer("http://oa:8100/mcp"), true},
		{"oa", plainServer("http://oa:8100/mcp"), false},
		{" oa ", passportServer("http://oa:8100/mcp"), true},
		{"oa-note", passportServer("http://x/mcp"), false},
		{"filesystem", passportServer("http://x/mcp"), false},
	}
	for _, tc := range cases {
		if got := isRetiredMCPServer(tc.name, tc.server); got != tc.want {
			t.Errorf("isRetiredMCPServer(%q, passport=%v) = %v, want %v",
				tc.name, mcpPassportEnabled(tc.server), got, tc.want)
		}
	}
}

// 装配时过滤是三道措施里唯一不依赖网络、也不依赖写盘的那道：基座没下架、本地清理
// 没跑成，退役的服务器照样连不上。
func TestFilterRetiredMCPServers(t *testing.T) {
	cfg := settings.MCPConfig{Servers: map[string]settings.MCPServerConfig{
		"oa":         passportServer("http://oa:8100/mcp"),
		"filesystem": plainServer("http://fs/mcp"),
	}}
	got, retired := filterRetiredMCPServers(cfg)

	if len(retired) != 1 || retired[0] != "oa" {
		t.Fatalf("retired = %v, want [oa]", retired)
	}
	if _, still := got.Servers["oa"]; still {
		t.Error("the retired server survived the filter; it would still connect")
	}
	if _, kept := got.Servers["filesystem"]; !kept {
		t.Error("an unrelated server was dropped")
	}
	// 原 map 不能被就地改掉：调用方拿到的是 settings.Load 的结果，那是它自己的东西。
	if _, ok := cfg.Servers["oa"]; !ok {
		t.Error("filterRetiredMCPServers mutated its input")
	}
}

func TestFilterRetiredMCPServersNoopWhenNothingRetired(t *testing.T) {
	cfg := settings.MCPConfig{Servers: map[string]settings.MCPServerConfig{
		"filesystem": plainServer("http://fs/mcp"),
	}}
	got, retired := filterRetiredMCPServers(cfg)
	if len(retired) != 0 {
		t.Fatalf("retired = %v, want none", retired)
	}
	if len(got.Servers) != 1 {
		t.Fatalf("servers = %v, want the input untouched", got.Servers)
	}
}

// 市场清单是一份可能落后于客户端的远端配置。一个没更新 ConfigMap 的部署会继续把 OA
// 摆在市场里让人一键装回来——装回来的正是那条绕过闸门的路。
func TestDropRetiredMarketEntries(t *testing.T) {
	entries := []McpMarketEntry{
		{ID: "oa", Name: "OA 办公助手"},
		{ID: "filesystem", Name: "文件系统"},
	}
	got := dropRetiredMarketEntries(entries)
	if len(got) != 1 || got[0].ID != "filesystem" {
		t.Fatalf("market = %v, want only filesystem", got)
	}
	// 输入不被就地改写（out 用的是零容量切片，不共享底层数组）。
	if len(entries) != 2 || entries[0].ID != "oa" {
		t.Fatal("dropRetiredMarketEntries mutated its input")
	}
}

// 清理是**删除**而不是停用：停用的条目在 MCP 页面上还是一个开关，用户随手打开就把
// 绕过闸门的那条路又接上了，而他不会知道这一点。
func TestRetireMCPServersRemovesEntry(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})

	if err := app.SaveMCPServer(MCPServerInput{
		Name: "oa", Transport: "http", URL: "http://oa:8100/mcp", Passport: true, Enabled: true,
	}); err != nil {
		t.Fatalf("装上 OA MCP: %v", err)
	}
	if err := app.SaveMCPServer(MCPServerInput{
		Name: "filesystem", Transport: "http", URL: "http://fs/mcp", Enabled: true,
	}); err != nil {
		t.Fatalf("装上另一台: %v", err)
	}

	app.retireMCPServers()

	servers, err := app.loadMCPServers()
	if err != nil {
		t.Fatalf("loadMCPServers: %v", err)
	}
	if _, still := servers["oa"]; still {
		t.Error("退役的条目还在 config.toml 里；用户会看到两套 OA 工具")
	}
	if _, kept := servers["filesystem"]; !kept {
		t.Error("无关的服务器被一并删掉了")
	}
}

// 幂等：第二次跑什么都不做（而不是报错或改坏别的条目）。
func TestRetireMCPServersIdempotent(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	if err := app.SaveMCPServer(MCPServerInput{
		Name: "filesystem", Transport: "http", URL: "http://fs/mcp", Enabled: true,
	}); err != nil {
		t.Fatalf("SaveMCPServer: %v", err)
	}
	app.retireMCPServers()
	app.retireMCPServers()

	servers, err := app.loadMCPServers()
	if err != nil {
		t.Fatalf("loadMCPServers: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers = %v, want the unrelated one intact", servers)
	}
}
