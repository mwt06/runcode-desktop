package desktop

import (
	"os"
	"testing"
)

// isolateConfigDir 把 os.UserConfigDir() 指到一个空的临时目录，返回**实际的**配置
// 根（不是 HOME）。
//
// 三个环境变量都要设，因为 UserConfigDir 在三个平台读的不是同一个东西：
//
//	Windows  %APPDATA%
//	Linux    $XDG_CONFIG_HOME（没有则 $HOME/.config）
//	macOS    $HOME/Library/Application Support —— 只认 HOME，APPDATA 与 XDG 都不看
//
// 少设一个的后果不是"测试在那个平台上失败"这么轻。此前这里只设 APPDATA 与
// XDG_CONFIG_HOME，于是**在 macOS 上隔离整个不生效**：测试读写的是开发者真实的
// 配置目录——自定义模型、通行证令牌、技能全在里面。CI 上表现为一批测试莫名其妙地
// 读到非空初始状态，而在本机跑一次的代价是配置被测试改掉。
//
// 返回值必须是解析后的路径而不是 HOME：macOS 上两者差着
// Library/Application Support 这一层，测试若按 HOME 去拼路径会找错地方。
func isolateConfigDir(t *testing.T) string {
	t.Helper()
	return isolateConfigDirAt(t, t.TempDir())
}

// isolateConfigDirAt 是 isolateConfigDir 的变体，用调用方指定的目录当家目录——
// 给那些需要先往里放东西、或者多个 App 要共享同一份配置的测试用。
func isolateConfigDirAt(t *testing.T, home string) string {
	t.Helper()
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	// 缓存目录一并隔离：appdirs 把 UserCacheDir 也算作"本应用自己的目录"（录音默认
	// 落在那儿，见 defaultRecorderRoot），漏掉它同样会让测试碰到真实路径。
	// macOS 的 UserCacheDir 也是从 HOME 推的，上面那行已经覆盖。
	t.Setenv("LOCALAPPDATA", home)   // Windows
	t.Setenv("XDG_CACHE_HOME", home) // Linux
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("隔离配置目录: %v", err)
	}
	// macOS 上这一层还不存在，而多数调用方拿到路径就直接往里写。
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("建配置目录 %s: %v", dir, err)
	}
	return dir
}
