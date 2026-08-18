package desktop

import "testing"

// SkipLogin (免登录) defaults off, is toggled through SaveSettings, and — being
// backend-owned — survives an ordinary session-start config save that carries no
// such field, so starting a session can never silently blank it.
func TestSaveSettingsPersistsSkipLoginAndCarriesForward(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})

	if app.LoadConfig().SkipLogin {
		t.Fatal("default SkipLogin should be false (login required)")
	}
	if _, err := app.SaveSettings(StartSessionRequest{SkipLogin: true, PermissionMode: "interactive"}); err != nil {
		t.Fatalf("SaveSettings enable: %v", err)
	}
	if !app.LoadConfig().SkipLogin {
		t.Fatal("SkipLogin not persisted after SaveSettings enable")
	}

	// An ordinary config write (as a session start does) must not blank it.
	saveConfig(StartSessionRequest{CWD: t.TempDir(), Provider: "openai", Model: "m"})
	if !app.LoadConfig().SkipLogin {
		t.Fatal("carry-forward failed: SkipLogin lost after a plain saveConfig")
	}

	if _, err := app.SaveSettings(StartSessionRequest{SkipLogin: false, PermissionMode: "interactive"}); err != nil {
		t.Fatalf("SaveSettings disable: %v", err)
	}
	if app.LoadConfig().SkipLogin {
		t.Fatal("SkipLogin not cleared after SaveSettings disable")
	}
}

// A live switch to a custom model persists it as the selected profile reference and
// clears passport-only state, while leaving unrelated saved settings untouched — so
// a restart reopens on this model and a later SaveSettings of other fields reads it
// through its persisted-profile override instead of reverting the connection.
func TestPersistConnectionChoiceCustomModel(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	if err := updateRawConfig(func(cfg *StartSessionRequest) error {
		cfg.Provider = "passport"
		cfg.Model = "glm-4.6"
		cfg.TenantID = "tenant-old"
		cfg.APIKeyProtected = "stale-ciphertext"
		cfg.PermissionMode = "judge"
		cfg.MaxContextTokens = 200_000
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	persistConnectionChoice("openai", "gpt-5", "GPT-5")

	got := loadRawConfig()
	if got.Provider != "openai" || got.Model != "gpt-5" || got.CustomModelName != "GPT-5" {
		t.Fatalf("connection identity = %+v, want openai/gpt-5/GPT-5", got)
	}
	// 这条断言**反转过**：原先要求 "custom switch must clear the passport tenant"，
	// 让持久化的连接快照自洽（自定义连接不经租户，就不带租户值）。代价是用户在对话内
	// 切一次自定义模型，已选租户就被清空，下次启动被迫重选——产品上不可接受。
	// 租户是账号级偏好（"我属于哪个组织"），不是连接的一部分；技术上留着也安全，
	// 因为只有 applyPassport 会读它去拼 Bridge 路径前缀，自定义连接直连自己的
	// Base URL，根本不看这个字段。
	if got.TenantID != "tenant-old" {
		t.Fatalf("自定义连接不该动账号级的租户选择: TenantID = %q, want %q", got.TenantID, "tenant-old")
	}
	if got.APIKeyProtected != "" || got.BaseURL != "" {
		t.Fatalf("custom switch must clear inline credentials/endpoint: %+v", got)
	}
	if got.PermissionMode != "judge" || got.MaxContextTokens != 200_000 {
		t.Fatalf("unrelated settings clobbered: mode=%q ctx=%d", got.PermissionMode, got.MaxContextTokens)
	}
}

// A live switch back to a platform model clears the custom-profile reference; the
// tenant is left exactly as it was, because SetActiveTenant is its only writer.
func TestPersistConnectionChoicePlatformModel(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	if err := updateRawConfig(func(cfg *StartSessionRequest) error {
		cfg.Provider = "openai"
		cfg.CustomModelName = "GPT-5"
		cfg.Model = "gpt-5"
		cfg.TenantID = "tenant-42"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	persistConnectionChoice("passport", "glm-4.6", "")

	got := loadRawConfig()
	if got.Provider != "passport" || got.Model != "glm-4.6" {
		t.Fatalf("connection identity = %+v, want passport/glm-4.6", got)
	}
	// 切回平台模型时租户同样不由这里写：SwitchModel 用的 nextTenant 就是
	// a.passportTenant，而它已经由 SetActiveTenant 同时落过盘与内存。少一个写入者，
	// "空到底是当前值还是无关"这种歧义就不会再出现。
	if got.TenantID != "tenant-42" {
		t.Fatalf("TenantID = %q, want %q（应由 SetActiveTenant 独占写入）", got.TenantID, "tenant-42")
	}
	if got.CustomModelName != "" {
		t.Fatalf("platform switch must clear the custom-profile reference, got %q", got.CustomModelName)
	}
}
