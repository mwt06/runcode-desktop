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

	persistConnectionChoice("openai", "gpt-5", "GPT-5", "")

	got := loadRawConfig()
	if got.Provider != "openai" || got.Model != "gpt-5" || got.CustomModelName != "GPT-5" {
		t.Fatalf("connection identity = %+v, want openai/gpt-5/GPT-5", got)
	}
	if got.TenantID != "" {
		t.Fatalf("custom switch must clear the passport tenant, got %q", got.TenantID)
	}
	if got.APIKeyProtected != "" || got.BaseURL != "" {
		t.Fatalf("custom switch must clear inline credentials/endpoint: %+v", got)
	}
	if got.PermissionMode != "judge" || got.MaxContextTokens != 200_000 {
		t.Fatalf("unrelated settings clobbered: mode=%q ctx=%d", got.PermissionMode, got.MaxContextTokens)
	}
}

// A live switch back to a platform model clears the custom-profile reference and
// records the target tenant, so the persisted connection matches the live session.
func TestPersistConnectionChoicePlatformModel(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	if err := updateRawConfig(func(cfg *StartSessionRequest) error {
		cfg.Provider = "openai"
		cfg.CustomModelName = "GPT-5"
		cfg.Model = "gpt-5"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	persistConnectionChoice("passport", "glm-4.6", "", "tenant-42")

	got := loadRawConfig()
	if got.Provider != "passport" || got.Model != "glm-4.6" || got.TenantID != "tenant-42" {
		t.Fatalf("connection identity = %+v, want passport/glm-4.6/tenant-42", got)
	}
	if got.CustomModelName != "" {
		t.Fatalf("platform switch must clear the custom-profile reference, got %q", got.CustomModelName)
	}
}
