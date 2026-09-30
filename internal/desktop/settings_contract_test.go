package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// 旧文件格式必须能完整读回；修改表单不应重写不属于它的连接/私有字段。
func TestSettingsLegacyStorageAndOwnedFields(t *testing.T) {
	isolateConfigDir(t)
	old := `{"cwd":"old-workspace","provider":"passport","model":"model-a","customModelName":"saved-profile","tenantId":"tenant-a","baseURL":"https://example.invalid","apiKeyProtected":"opaque-key","authTokenProtected":"opaque-token","permissionMode":"interactive","thinkingEffort":"high","reasoningScenario":"auto","recentWorkspaces":["old-workspace"],"customModels":[{"name":"local","model":"m","apiKeyProtected":"profile-secret"}],"webProxy":"http://localhost:9999","skipLogin":false,"contextAudit":true,"maxTokens":1024,"maxContextTokens":260000,"maxHistoryMessages":12}`
	path, err := desktopConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	before := loadRawConfig()
	app := New(&recordingSink{})
	if _, err := app.SaveSettings(SaveSettingsRequest{PermissionMode: "judge", SkipLogin: true, MaxTokens: 0, MaxContextTokens: 0, MaxHistoryMessages: 0, HarmJudgeModel: "judge-model", HarmJudgeVotes: 3}); err != nil {
		t.Fatal(err)
	}
	got := loadRawConfig()
	want := before
	want.PermissionMode, want.SkipLogin = "judge", true
	want.MaxTokens, want.MaxContextTokens, want.MaxHistoryMessages = 0, 0, 0
	want.HarmJudgeModel, want.HarmJudgeVotes = "judge-model", 3
	if !reflect.DeepEqual(got, want) {
		t.Fatal("settings changed fields outside its ownership")
	}
	data, err := json.Marshal(app.LoadConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"apiKey", "authToken", "apiKeyProtected", "authTokenProtected", "customModels", "opaque-key", "opaque-token", "profile-secret"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("view exposed %s", forbidden)
		}
	}
	saveConfig(StartSessionRequest{CWD: "next", Provider: "openai", Model: "next"})
	got = loadRawConfig()
	if !got.ContextAudit || !got.SkipLogin || got.TenantID != before.TenantID || got.WebProxy != before.WebProxy || !reflect.DeepEqual(got.CustomModels, before.CustomModels) {
		t.Fatal("session save clobbered application config")
	}
	if got.MaxContextTokens != 0 || got.MaxTokens != 0 || got.MaxHistoryMessages != 0 {
		t.Fatal("explicit zero settings did not survive session save")
	}
}

func TestSaveSettingsFailureDoesNotChangeNextSession(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	before := app.config.MaxTokens
	path, err := desktopConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	// The destination is a directory: atomic replacement must fail on all platforms.
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveSettings(SaveSettingsRequest{PermissionMode: "judge", MaxTokens: 1234}); err == nil {
		t.Fatal("failed persistence reported success")
	}
	if app.config.MaxTokens != before {
		t.Fatal("failed save mutated next session")
	}
}

func TestSaveSettingsRejectsInvalidInputWithoutWriting(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	for _, req := range []SaveSettingsRequest{{PermissionMode: "unknown"}, {PermissionMode: "interactive", MaxTokens: -1}} {
		if _, err := app.SaveSettings(req); err == nil {
			t.Fatal("invalid settings accepted")
		}
	}
	if _, ok := loadRawConfigOK(); ok {
		t.Fatal("invalid settings reached disk")
	}
}

func TestSettingsAndOtherConfigWritersPreserveEachOther(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			if _, err := app.SaveSettings(SaveSettingsRequest{PermissionMode: "interactive", MaxTokens: 500, SkipLogin: true}); err != nil {
				t.Error(err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			if err := updateRawConfig(func(c *desktopConfig) error { c.TenantID = "tenant"; c.ContextAudit = true; return nil }); err != nil {
				t.Error(err)
			}
		}
	}()
	wg.Wait()
	got := loadRawConfig()
	if got.TenantID != "tenant" || !got.ContextAudit || got.MaxTokens != 500 || !got.SkipLogin {
		t.Fatal("concurrent configuration writes lost fields")
	}
}
