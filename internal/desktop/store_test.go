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
	t.Setenv("APPDATA", t.TempDir()) // 隔离 desktop.json（Windows: os.UserConfigDir 读 APPDATA）
	app := New(&recordingSink{})

	const n = 20
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := app.SaveCustomModel(CustomModel{Name: fmt.Sprintf("m%02d", i), Model: "x"}); err != nil {
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
	t.Setenv("APPDATA", t.TempDir())
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
