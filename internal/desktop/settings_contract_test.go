package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	wireprotocol "github.com/wt68/runcode/internal/protocol"
	engineprotocol "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
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
	if _, err := app.SaveSettings(SaveSettingsRequest{PermissionMode: "judge", SkipLogin: true, HarmJudgeModel: "judge-model", HarmJudgeVotes: 3}); err != nil {
		t.Fatal(err)
	}
	got := loadRawConfig()
	want := before
	want.PermissionMode, want.SkipLogin = "judge", true
	want.HarmJudgeModel, want.HarmJudgeVotes = "judge-model", 3
	if !reflect.DeepEqual(got, want) {
		t.Fatal("settings changed fields outside its ownership")
	}
	data, err := json.Marshal(app.LoadConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"apiKey", "authToken", "apiKeyProtected", "authTokenProtected", "customModels", "opaque-key", "opaque-token", "profile-secret", "maxTokens", "maxContextTokens", "maxHistoryMessages"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("view exposed %s", forbidden)
		}
	}
	saveConfig(StartSessionRequest{CWD: "next", Provider: "openai", Model: "next"})
	got = loadRawConfig()
	if !got.ContextAudit || !got.SkipLogin || got.TenantID != before.TenantID || got.WebProxy != before.WebProxy || !reflect.DeepEqual(got.CustomModels, before.CustomModels) {
		t.Fatalf("session save clobbered application config: audit=%v login=%v tenant=%v proxy=%v custom=%v", got.ContextAudit, got.SkipLogin, got.TenantID == before.TenantID, got.WebProxy == before.WebProxy, reflect.DeepEqual(got.CustomModels, before.CustomModels))
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"maxTokens", "maxContextTokens", "maxHistoryMessages"} {
		if strings.Contains(string(data), key) {
			t.Fatalf("obsolete stored setting survived: %s", key)
		}
	}
}

func TestSaveSettingsFailureDoesNotChangeNextSession(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	before := app.config.PermissionMode
	path, err := desktopConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	// The destination is a directory: atomic replacement must fail on all platforms.
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveSettings(SaveSettingsRequest{PermissionMode: "judge", HarmJudgeVotes: 3}); err == nil {
		t.Fatal("failed persistence reported success")
	}
	if app.config.PermissionMode != before {
		t.Fatal("failed save mutated next session")
	}
}

func TestSaveSettingsRejectsInvalidInputWithoutWriting(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	for _, req := range []SaveSettingsRequest{{PermissionMode: "unknown"}, {PermissionMode: "interactive", HarmJudgeVotes: -1}} {
		if _, err := app.SaveSettings(req); err == nil {
			t.Fatal("invalid settings accepted")
		}
	}
	if _, ok := loadRawConfigOK(); ok {
		t.Fatal("invalid settings reached disk")
	}
}

func TestSaveSettingsExplicitEmptySessionOnlyWritesDefaults(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	focusOn(app, "focused", t.TempDir())
	empty := ""

	info, err := app.SaveSettings(SaveSettingsRequest{
		SessionID:      &empty,
		PermissionMode: "judge",
		HarmJudgeVotes: 3,
		SkipLogin:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.SessionID != "" {
		t.Fatalf("defaults-only save returned focused session %q", info.SessionID)
	}
	got := loadRawConfig()
	if got.PermissionMode != "judge" || got.HarmJudgeVotes != 3 || !got.SkipLogin {
		t.Fatalf("defaults-only fields not persisted: %+v", got)
	}
}

func TestSaveSettingsRejectsClosedExplicitSessionBeforeWriting(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	entry := focusOn(app, "closed", t.TempDir())
	app.mu.Lock()
	entry.closed = true
	app.mu.Unlock()
	id := "closed"

	_, err := app.SaveSettings(SaveSettingsRequest{
		SessionID:      &id,
		PermissionMode: "judge",
		HarmJudgeVotes: 3,
	})
	var pe *engineprotocol.Error
	if !errors.As(err, &pe) || pe.Code != engineprotocol.ErrCodeNoSession {
		t.Fatalf("closed target error = %v, want no_session", err)
	}
	if _, ok := loadRawConfigOK(); ok {
		t.Fatal("closed target reached disk")
	}
}

func TestSaveSettingsReportsApplyFailureAfterPersistence(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	focusOn(app, "live-without-manager", t.TempDir())
	id := "live-without-manager"

	_, err := app.SaveSettings(SaveSettingsRequest{
		SessionID:      &id,
		PermissionMode: "judge",
		HarmJudgeVotes: 5,
	})
	var pe *engineprotocol.Error
	if !errors.As(err, &pe) || pe.Code != wireprotocol.ErrCodeSettingsApplyFailed {
		t.Fatalf("apply failure = %v, want settings_apply_failed", err)
	}
	got := loadRawConfig()
	if got.PermissionMode != "judge" || got.HarmJudgeVotes != 5 {
		t.Fatalf("persistence should have succeeded before apply failure: %+v", got)
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
			if _, err := app.SaveSettings(SaveSettingsRequest{PermissionMode: "interactive", HarmJudgeVotes: 5, SkipLogin: true}); err != nil {
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
	if got.TenantID != "tenant" || !got.ContextAudit || got.HarmJudgeVotes != 5 || !got.SkipLogin {
		t.Fatal("concurrent configuration writes lost fields")
	}
}
