package desktop

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestMergeRecentWorkspaces(t *testing.T) {
	t.Parallel()

	t.Run("promotes current workspace to front", func(t *testing.T) {
		got := mergeRecentWorkspaces([]string{"/a", "/b"}, "/b")
		if strings.Join(got, ",") != "/b,/a" {
			t.Fatalf("got %v, want [/b /a]", got)
		}
	})

	t.Run("prepends a new workspace", func(t *testing.T) {
		got := mergeRecentWorkspaces([]string{"/a", "/b"}, "/c")
		if strings.Join(got, ",") != "/c,/a,/b" {
			t.Fatalf("got %v, want [/c /a /b]", got)
		}
	})

	t.Run("empty cwd leaves the list unchanged", func(t *testing.T) {
		got := mergeRecentWorkspaces([]string{"/a", "/b"}, "")
		if strings.Join(got, ",") != "/a,/b" {
			t.Fatalf("got %v, want [/a /b]", got)
		}
	})

	t.Run("caps the list length", func(t *testing.T) {
		prev := make([]string, maxRecentWorkspaces+4)
		for i := range prev {
			prev[i] = string(rune('a' + i))
		}
		got := mergeRecentWorkspaces(prev, "/new")
		if len(got) != maxRecentWorkspaces {
			t.Fatalf("len = %d, want %d", len(got), maxRecentWorkspaces)
		}
		if got[0] != "/new" {
			t.Fatalf("front = %q, want /new", got[0])
		}
	})

	t.Run("drops blank prior entries", func(t *testing.T) {
		got := mergeRecentWorkspaces([]string{"", "/a", ""}, "/a")
		if strings.Join(got, ",") != "/a" {
			t.Fatalf("got %v, want [/a]", got)
		}
	})
}

// 并发回归：desktop.json 的所有变更方法都是"整读→改→整写"，靠 configMu 串行化。
// 三路写入方各改各的字段并发跑，结束后任何一路的改动都不能被别人的旧快照覆盖。
// 修复前（无锁 + saveRawConfig 直写）本测试会因丢更新而不稳定地失败。
func TestConfigMutatorsConcurrentNoLostUpdate(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})

	const n = 20
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := app.SaveCustomModel(SaveCustomModelRequest{Name: fmt.Sprintf("m%02d", i), Model: "x"}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := app.SetWebProxy("http://127.0.0.1:7890"); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			app.persistThinkingEffort("high")
		}
	}()
	wg.Wait()

	if got := len(app.ListCustomModels()); got != n {
		t.Fatalf("custom models = %d, want %d（有并发写被旧快照覆盖）", got, n)
	}
	if got := app.WebProxy(); got != "http://127.0.0.1:7890" {
		t.Fatalf("web proxy = %q, want persisted value", got)
	}
	if got := app.LoadConfig().ThinkingEffort; got != "high" {
		t.Fatalf("thinking effort = %q, want %q", got, "high")
	}
}

// 并发回归：用户级 disabled.json 的开关同样是读改写循环，靠 disabledMu 串行化。
// 并发关闭 n 个不同工具，结束后必须全部在关闭名单里。
func TestSetDisabledConcurrentNoLostUpdate(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})

	const n = 16
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			if err := app.SetToolEnabled(fmt.Sprintf("tool%02d", i), "user", false); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	tools, _, _ := effectiveDisabled("")
	if len(tools) != n {
		t.Fatalf("disabled tools = %d (%v), want %d", len(tools), tools, n)
	}
}

// 租户是账号级选择，只有 SetActiveTenant 能改。会话级的 saveConfig 不得清空它：
// 自定义连接的启动请求天然不带 TenantID（直连自己的 Base URL，不经租户），一旦原样
// 落盘就会把已选租户抹掉，多租户用户下次启动被迫重选——这正是"每次打开都要重新选
// 租户和模型"的后半截。
func TestSaveConfigKeepsTenantWhenRequestHasNone(t *testing.T) {
	isolateConfigDir(t)

	// 用户选定了租户（走 SetActiveTenant 的持久化路径）。
	if err := updateRawConfig(func(raw *StartSessionRequest) error {
		raw.TenantID = "wjtest"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 随后用一个自定义连接开会话：请求里没有租户。
	saveConfig(StartSessionRequest{
		CWD: t.TempDir(), Provider: "anthropic", Model: "claude-opus-5", CustomModelName: "claude-opus-5",
	})

	if got := loadRawConfig().TenantID; got != "wjtest" {
		t.Fatalf("自定义连接的会话把租户抹掉了: TenantID = %q, want %q", got, "wjtest")
	}
}

// 反向：请求带了租户就该覆盖，否则切换租户后开会话会被旧值粘住。
func TestSaveConfigOverwritesTenantWhenRequestHasOne(t *testing.T) {
	isolateConfigDir(t)

	if err := updateRawConfig(func(raw *StartSessionRequest) error {
		raw.TenantID = "old"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	saveConfig(StartSessionRequest{CWD: t.TempDir(), Provider: "passport", Model: "m", TenantID: "new"})

	if got := loadRawConfig().TenantID; got != "new" {
		t.Fatalf("TenantID = %q, want %q", got, "new")
	}
}

// TestDefaultRequestArmsCompaction 守「自动总结默认开着」这条产品约定：预算是 0 就等于
// 关掉自动压缩，长会话会一路涨到模型窗口上限然后每次请求都被拒。
//
// 数值一并钉死：设置页的下拉只列出固定几档，默认值必须正好是其中一档，否则那个
// SelectField 会渲染成空白（值不在 option 里），用户看到的是「没设置」。
func TestDefaultRequestArmsCompaction(t *testing.T) {
	t.Parallel()

	if got := defaultRequest().MaxContextTokens; got != 260_000 {
		t.Fatalf("默认上下文预算 = %d, want 260000（0 等于关掉自动总结）", got)
	}
}

// TestContextLimitsAreSettingsOwned 守一个实测到的回归：在设置里填了「最大输出 Tokens」，
// 重启后从起始页进一次会话，值就没了。
//
// 根因是起始页不渲染这三项、按 wire 零值发过来，而 saveConfig 把零值原样落盘 ——
// 每开一次新会话就清一次。现在 saveConfig 对它们一律沿用旧值，SaveSettings（经
// updateRawConfig）是唯一的写入口。
func TestContextLimitsAreSettingsOwned(t *testing.T) {
	isolateConfigDir(t)

	// 还没有配置文件时，请求自带的值就是事实 —— 否则首次保存会把 defaultRequest
	// 播下的上下文预算抹成 0，等于默认关掉自动总结。
	saveConfig(StartSessionRequest{
		CWD: "/ws", Model: "m",
		MaxTokens: 8192, MaxContextTokens: 260_000, MaxHistoryMessages: 40,
	})
	if got := loadRawConfig(); got.MaxTokens != 8192 || got.MaxContextTokens != 260_000 || got.MaxHistoryMessages != 40 {
		t.Fatalf("首次保存 = %d/%d/%d, want 8192/260000/40", got.MaxTokens, got.MaxContextTokens, got.MaxHistoryMessages)
	}

	// 起始页开会话：那三项发零值，一个都不能被清掉。
	saveConfig(StartSessionRequest{CWD: "/ws", Model: "m"})
	got := loadRawConfig()
	if got.MaxTokens != 8192 {
		t.Fatalf("最大输出 = %d, want 8192（起始页的零值不该清掉设置里填的）", got.MaxTokens)
	}
	if got.MaxContextTokens != 260_000 || got.MaxHistoryMessages != 40 {
		t.Fatalf("上下文预算/历史上限 = %d/%d, want 260000/40", got.MaxContextTokens, got.MaxHistoryMessages)
	}

	// 新会话必须真的用上落盘的那份，否则「丢设置」修好了、「不生效」还在。
	req := withStoredContextLimits(StartSessionRequest{CWD: "/ws", Model: "m"})
	if req.MaxTokens != 8192 || req.MaxContextTokens != 260_000 || req.MaxHistoryMessages != 40 {
		t.Fatalf("withStoredContextLimits = %d/%d/%d, want 8192/260000/40", req.MaxTokens, req.MaxContextTokens, req.MaxHistoryMessages)
	}
	if cfg, err := buildConfig(withStoredContextLimits(StartSessionRequest{CWD: t.TempDir(), Model: "m"})); err != nil {
		t.Fatalf("buildConfig: %v", err)
	} else if cfg.MaxTokens != 8192 {
		t.Fatalf("会话 MaxTokens = %d, want 8192", cfg.MaxTokens)
	}

	// 设置页把某项清空（= 回默认）也得留得住：沿用规则不能把它倒回旧值。
	if err := updateRawConfig(func(cfg *StartSessionRequest) error {
		cfg.MaxTokens = 0
		return nil
	}); err != nil {
		t.Fatalf("updateRawConfig: %v", err)
	}
	saveConfig(StartSessionRequest{CWD: "/ws", Model: "m"})
	if got := loadRawConfig().MaxTokens; got != 0 {
		t.Fatalf("清空后 = %d, want 0（设置页是唯一写入口，清空必须生效）", got)
	}
}

// TestWithStoredContextLimitsKeepsSeedWithoutConfig 守首次启动：还没有 desktop.json 时
// 沿用「磁盘上的值」会把种子默认抹成 0 —— 上下文预算为 0 就是自动总结没开。
func TestWithStoredContextLimitsKeepsSeedWithoutConfig(t *testing.T) {
	isolateConfigDir(t)

	req := withStoredContextLimits(defaultRequest())
	if req.MaxContextTokens != defaultRequest().MaxContextTokens {
		t.Fatalf("无配置文件时 = %d, want 种子值 %d", req.MaxContextTokens, defaultRequest().MaxContextTokens)
	}
}
