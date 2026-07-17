package desktop

import "testing"

func TestNormalizeProxy(t *testing.T) {
	ok := map[string]string{
		"":                        "",
		"  ":                      "",
		"127.0.0.1:7890":          "http://127.0.0.1:7890", // 省略协议按 http
		"http://127.0.0.1:7890":   "http://127.0.0.1:7890",
		"https://p.example:8443":  "https://p.example:8443",
		"socks5://127.0.0.1:1080": "socks5://127.0.0.1:1080",
	}
	for in, want := range ok {
		got, err := normalizeProxy(in)
		if err != nil {
			t.Errorf("normalizeProxy(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeProxy(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"http://", "ftp://h:1", "://x"} {
		if got, err := normalizeProxy(bad); err == nil {
			t.Errorf("normalizeProxy(%q) = %q, want error", bad, got)
		}
	}
}

func TestSetWebProxyPersists(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	if got := app.WebProxy(); got != "" {
		t.Fatalf("initial = %q, want empty", got)
	}
	if _, err := app.SetWebProxy("127.0.0.1:7890"); err != nil {
		t.Fatal(err)
	}
	if got := app.WebProxy(); got != "http://127.0.0.1:7890" {
		t.Fatalf("after set = %q", got)
	}
	// 起会话保存表单不能把它抹掉(与 CustomModels 一样由后端持有)。
	saveConfig(StartSessionRequest{CWD: t.TempDir(), Provider: "openai", Model: "m"})
	if got := app.WebProxy(); got != "http://127.0.0.1:7890" {
		t.Fatalf("after saveConfig = %q, want preserved", got)
	}
	if _, err := app.SetWebProxy(""); err != nil {
		t.Fatal(err)
	}
	if got := app.WebProxy(); got != "" {
		t.Fatalf("after clear = %q", got)
	}
}

// SetWebProxy 不再改进程环境变量；生效通道是 buildConfig 把持久化值注入
// engine.Config.WebProxy（按会话隔离，下个新建/恢复会话采用）。
func TestBuildConfigInjectsWebProxy(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	if _, err := app.SetWebProxy("127.0.0.1:7890"); err != nil {
		t.Fatal(err)
	}
	cfg, err := buildConfig(StartSessionRequest{CWD: t.TempDir(), Provider: "openai", Model: "m"})
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.WebProxy != "http://127.0.0.1:7890" {
		t.Fatalf("cfg.WebProxy = %q, want the persisted proxy", cfg.WebProxy)
	}
	// 清除后新配置回到直连。
	if _, err := app.SetWebProxy(""); err != nil {
		t.Fatal(err)
	}
	cfg, err = buildConfig(StartSessionRequest{CWD: t.TempDir(), Provider: "openai", Model: "m"})
	if err != nil {
		t.Fatalf("buildConfig after clear: %v", err)
	}
	if cfg.WebProxy != "" {
		t.Fatalf("cfg.WebProxy after clear = %q, want empty", cfg.WebProxy)
	}
}
