package desktop

import (
	"os"
	"strings"
	"testing"
	"time"

	appwire "github.com/wt68/runcode/internal/protocol"
)

// imageGenSettingsApp：提供 fixture 目录并以 sub 登录。
//
//nolint:unparam // 夹具保留 sub，便于以后换账号而不改调用点签名。
func imageGenSettingsApp(t *testing.T, sub string) *App {
	t.Helper()
	isolateConfigDir(t)
	fx := loadCatalogFixture(t)
	serveCatalog(t, 200, fx.Response)
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT(sub), Expiry: time.Now().Add(time.Hour)})
	return app
}

//nolint:unparam // 夹具保留 tenant，跨租户用例可直接改实参。
func saveImageModel(t *testing.T, app *App, tenant, model, size string) appwire.ImageGenerationSettings {
	t.Helper()
	cur, err := app.GetImageGenerationSettings(tenant)
	if err != nil {
		t.Fatal(err)
	}
	ref := platformModelRef(tenant, model)
	got, err := app.SaveImageGenerationSettings(appwire.SaveImageGenerationSettingsRequest{
		TenantID: tenant, ScopeID: cur.ScopeID, ExpectedRevision: cur.Revision, DefaultModel: &ref, DefaultSize: size,
	})
	if err != nil {
		t.Fatalf("save %s: %v", model, err)
	}
	return got
}

func TestImageGenerationSettingsKeepsEmptyDefaultSize(t *testing.T) {
	app := imageGenSettingsApp(t, "user-a")
	// image-only-b64 支持多种尺寸，空值表示不固定。
	saved := saveImageModel(t, app, "tenant-a", "image-only-b64", "")
	if saved.Revision != 1 || saved.DefaultModel == nil || saved.DefaultModel.Name != "image-only-b64" || saved.DefaultSize != "" {
		t.Fatalf("saved = %+v (want empty default size)", saved)
	}
	fixed := saveImageModel(t, app, "tenant-a", "image-only-b64", "1280x720")
	if fixed.DefaultSize != "1280x720" || fixed.Revision != 2 {
		t.Fatalf("fixed size = %+v", fixed)
	}
	cleared := saveImageModel(t, app, "tenant-a", "image-only-b64", "")
	if cleared.DefaultSize != "" || cleared.Revision != 3 {
		t.Fatalf("cleared size = %+v", cleared)
	}
	// 仅一种尺寸的模型：空值保存时钉成该尺寸，不提供不固定。
	single := saveImageModel(t, app, "tenant-a", "image-only-url", "")
	if single.DefaultSize != "2048x2048" {
		t.Fatalf("single-size model empty save = %+v", single)
	}
}

func TestImageGenerationSettingsPartitionByAccountAndTenant(t *testing.T) {
	app := imageGenSettingsApp(t, "user-a")
	saved := saveImageModel(t, app, "tenant-a", "image-only-url", "2048x2048")
	if saved.Revision != 1 || saved.DefaultModel == nil || saved.DefaultModel.Name != "image-only-url" || saved.DefaultSize != "2048x2048" {
		t.Fatalf("saved = %+v (want fixed size)", saved)
	}
	other, err := app.GetImageGenerationSettings("tenant-b")
	if err != nil || other.DefaultModel != nil || other.ScopeID == saved.ScopeID {
		t.Fatalf("tenant-b inherited tenant-a: %+v %v", other, err)
	}
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("user-b"), Expiry: time.Now().Add(time.Hour)})
	userB, err := app.GetImageGenerationSettings("tenant-a")
	if err != nil || userB.DefaultModel != nil || userB.ScopeID == saved.ScopeID {
		t.Fatalf("user-b inherited user-a: %+v %v", userB, err)
	}
	saveImageModel(t, app, "tenant-a", "image-only-b64", "1280x720")
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("user-a"), Expiry: time.Now().Add(time.Hour)})
	back, err := app.GetImageGenerationSettings("tenant-a")
	if err != nil || back.DefaultModel == nil || back.DefaultModel.Name != "image-only-url" {
		t.Fatalf("user-a lost its own default: %+v %v", back, err)
	}
}

func TestImageGenerationSettingsRejectUnauthorizedTargets(t *testing.T) {
	app := imageGenSettingsApp(t, "user-a")
	cur, err := app.GetImageGenerationSettings("tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	bad := []struct {
		name string
		ref  appwire.ModelReference
		size string
	}{
		{"not in catalog", platformModelRef("tenant-a", "missing-model"), ""},
		{"unavailable", platformModelRef("tenant-a", "image-unavailable"), ""},
		{"chat model", platformModelRef("tenant-a", "chat-legacy"), ""},
		{"unsupported size", platformModelRef("tenant-a", "image-only-url"), "1024x1024"},
		{"other tenant ref", platformModelRef("tenant-b", "image-only-url"), ""},
		{"custom ref", appwire.ModelReference{Kind: "custom", Name: "image-only-url"}, ""},
		{"foreign bridge", appwire.ModelReference{Kind: "platform", Name: "image-only-url", Bridge: "https://evil.invalid", TenantID: "tenant-a"}, ""},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			ref := tc.ref
			if _, err := app.SaveImageGenerationSettings(appwire.SaveImageGenerationSettingsRequest{TenantID: "tenant-a", ScopeID: cur.ScopeID, ExpectedRevision: 0, DefaultModel: &ref, DefaultSize: tc.size}); err == nil {
				t.Fatal("unauthorized target saved")
			}
		})
	}
	after, _ := app.GetImageGenerationSettings("tenant-a")
	if after.Revision != 0 || after.DefaultModel != nil {
		t.Fatalf("rejected saves changed state: %+v", after)
	}
	if _, err := app.SaveImageGenerationSettings(appwire.SaveImageGenerationSettingsRequest{TenantID: "tenant-a", ScopeID: "stale-scope", DefaultModel: nil}); err == nil {
		t.Fatal("stale scope accepted")
	}
}

func TestImageGenerationSettingsRevisionAndIdempotence(t *testing.T) {
	app := imageGenSettingsApp(t, "user-a")
	first := saveImageModel(t, app, "tenant-a", "image-only-url", "2048x2048")
	ref := platformModelRef("tenant-a", "image-only-url")
	again, err := app.SaveImageGenerationSettings(appwire.SaveImageGenerationSettingsRequest{TenantID: "tenant-a", ScopeID: first.ScopeID, ExpectedRevision: 0, DefaultModel: &ref, DefaultSize: "2048x2048"})
	if err != nil || again.Revision != first.Revision {
		t.Fatalf("same target with stale revision not idempotent: %+v %v", again, err)
	}
	other := platformModelRef("tenant-a", "image-only-b64")
	if _, err := app.SaveImageGenerationSettings(appwire.SaveImageGenerationSettingsRequest{TenantID: "tenant-a", ScopeID: first.ScopeID, ExpectedRevision: 0, DefaultModel: &other}); err == nil {
		t.Fatal("different target with stale revision accepted")
	}
	cleared, err := app.SaveImageGenerationSettings(appwire.SaveImageGenerationSettingsRequest{TenantID: "tenant-a", ScopeID: first.ScopeID, ExpectedRevision: first.Revision})
	if err != nil || cleared.DefaultModel != nil || cleared.DefaultSize != "" || cleared.Revision != first.Revision+1 {
		t.Fatalf("clear = %+v %v", cleared, err)
	}
}

func TestImageGenerationSettingsFailurePreservesConfirmedValue(t *testing.T) {
	app := imageGenSettingsApp(t, "user-a")
	first := saveImageModel(t, app, "tenant-a", "image-only-url", "")
	path, err := desktopConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// 目标路径变成目录：各平台上原子替换都会失败。
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	ref := platformModelRef("tenant-a", "image-only-b64")
	if _, err := app.SaveImageGenerationSettings(appwire.SaveImageGenerationSettingsRequest{TenantID: "tenant-a", ScopeID: first.ScopeID, ExpectedRevision: first.Revision, DefaultModel: &ref}); err == nil {
		t.Fatal("failed persistence reported success")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := app.GetImageGenerationSettings("tenant-a")
	if err != nil || got.Revision != first.Revision || got.DefaultModel == nil || got.DefaultModel.Name != "image-only-url" {
		t.Fatalf("failed save changed confirmed value: %+v %v", got, err)
	}
}

func TestImageGenerationSettingsSurviveOtherSaversAndRestart(t *testing.T) {
	app := imageGenSettingsApp(t, "user-a")
	saved := saveImageModel(t, app, "tenant-a", "image-only-url", "")
	if _, err := app.SaveSettings(SaveSettingsRequest{PermissionMode: "interactive", HarmJudgeVotes: 1}); err != nil {
		t.Fatal(err)
	}
	saveConfig(StartSessionRequest{CWD: t.TempDir(), Provider: "passport", TenantID: "tenant-a"})
	if err := updateRawConfig(func(c *desktopConfig) error { c.WebProxy = "http://127.0.0.1:1"; return nil }); err != nil {
		t.Fatal(err)
	}
	restarted := New(&recordingSink{})
	restarted.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("user-a"), Expiry: time.Now().Add(time.Hour)})
	got, err := restarted.GetImageGenerationSettings("tenant-a")
	if err != nil || got.Revision != saved.Revision || got.DefaultModel == nil || *got.DefaultModel != *saved.DefaultModel || got.DefaultSize != saved.DefaultSize {
		t.Fatalf("settings lost by other writers/restart: %+v %v", got, err)
	}
	if loadRawConfig().Model != "" {
		t.Fatal("saving an image model changed the main chat model")
	}
}

func TestImageGenerationSettingsRequireStableLogin(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	if _, err := app.GetImageGenerationSettings("tenant-a"); err == nil {
		t.Fatal("anonymous settings read")
	}
	app.tokens.setInMemory(tokenSet{AccessToken: "opaque-token", Expiry: time.Now().Add(time.Hour)})
	if _, err := app.GetImageGenerationSettings("tenant-a"); err == nil || !strings.Contains(err.Error(), "稳定身份") {
		t.Fatalf("opaque token partitioned settings: %v", err)
	}
}
