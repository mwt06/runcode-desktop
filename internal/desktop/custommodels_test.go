package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

// TestCustomModelResponsesProvider covers OpenAI's second wire protocol end to
// end through the desktop: the form value survives the save/list round trip and
// expands into the engine request. It also pins the gate to the engine registry
// — what this package accepts must be exactly what BuildProvider can construct,
// not a list of strings that can drift from it.
func TestCustomModelResponsesProvider(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})

	list, err := app.SaveCustomModel(SaveCustomModelRequest{
		Name: "GPT-5", Provider: " OpenAI-Responses ", Model: "gpt-5", BaseURL: "https://api.openai.com/v1",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(list) != 1 || list[0].Provider != "openai-responses" {
		t.Fatalf("list = %+v", list)
	}

	req, err := app.resolveCustomModelRequest(StartSessionRequest{CWD: "workspace", CustomModelName: "GPT-5"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if req.Provider != "openai-responses" || req.Model != "gpt-5" || req.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("resolved request = %+v", req)
	}
	if !llm.IsRegistered(req.Provider) {
		t.Fatalf("%q is not an engine provider; registered = %v", req.Provider, llm.Registered())
	}
}

// TestUnsupportedProviderErrorNamesAlternatives keeps the rejection actionable:
// a typo should tell the user what the valid ids actually are.
func TestUnsupportedProviderErrorNamesAlternatives(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	_, err := app.SaveCustomModel(SaveCustomModelRequest{Name: "n", Provider: "openai-response", Model: "m"})
	if err == nil {
		t.Fatal("want a near-miss provider id rejected")
	}
	for _, want := range []string{"openai-responses", "anthropic"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should list %q", err.Error(), want)
		}
	}
}

func TestCustomModelsCRUDRoundTrip(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	if got := app.ListCustomModels(); len(got) != 0 {
		t.Fatalf("initial = %+v, want empty", got)
	}

	list, err := app.SaveCustomModel(SaveCustomModelRequest{
		Name: "本地 Ollama", Provider: " OpenAI ", Model: "qwen2.5-coder", BaseURL: " http://localhost:11434/v1 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "本地 Ollama" || list[0].Provider != "openai" || list[0].BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("after save = %+v", list)
	}
	if list[0].APIKey != "" || list[0].APIKeyProtected != "" || list[0].HasAPIKey {
		t.Fatalf("list leaked or invented key state: %+v", list[0])
	}

	list, err = app.SaveCustomModel(SaveCustomModelRequest{
		OriginalName: "本地 Ollama", Name: "本地 Ollama", Provider: "anthropic", Model: "claude-test", BaseURL: "https://example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Provider != "anthropic" || list[0].Model != "claude-test" {
		t.Fatalf("after edit = %+v", list)
	}

	list, err = app.DeleteCustomModel(" 本地 Ollama ")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("after delete = %+v", list)
	}
}

func TestSaveCustomModelProviderAndInputValidation(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	for _, tc := range []struct {
		name string
		req  SaveCustomModelRequest
	}{
		{name: "empty name", req: SaveCustomModelRequest{Name: "", Model: "m"}},
		{name: "empty model", req: SaveCustomModelRequest{Name: "n", Model: ""}},
		{name: "unknown provider", req: SaveCustomModelRequest{Name: "n", Provider: "azure", Model: "m"}},
		{name: "replace and clear key", req: SaveCustomModelRequest{Name: "n", Model: "m", APIKey: "secret", ClearAPIKey: true}},
		{name: "clear while creating", req: SaveCustomModelRequest{Name: "n", Model: "m", ClearAPIKey: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := app.SaveCustomModel(tc.req); err == nil {
				t.Fatal("want error")
			}
		})
	}
	if got := app.ListCustomModels(); len(got) != 0 {
		t.Fatalf("invalid saves changed storage: %+v", got)
	}
}

func TestSaveCustomModelRenamesAndRejectsConflict(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	for _, req := range []SaveCustomModelRequest{
		{Name: "A", Model: "model-a"},
		{Name: "B", Provider: "anthropic", Model: "model-b"},
	} {
		if _, err := app.SaveCustomModel(req); err != nil {
			t.Fatal(err)
		}
	}
	if err := updateRawConfig(func(cfg *StartSessionRequest) error {
		cfg.CustomModelName = "A"
		cfg.Provider = "openai"
		cfg.Model = "model-a"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := app.SaveCustomModel(SaveCustomModelRequest{OriginalName: "A", Name: "B", Model: "replacement"}); err == nil {
		t.Fatal("want conflict when renaming A to existing B")
	}
	list := app.ListCustomModels()
	if len(list) != 2 || list[0].Name != "A" || list[0].Model != "model-a" || list[1].Name != "B" || list[1].Model != "model-b" {
		t.Fatalf("conflicting rename changed records: %+v", list)
	}

	list, err := app.SaveCustomModel(SaveCustomModelRequest{OriginalName: "A", Name: "C", Provider: "anthropic", Model: "model-c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "C" || list[0].Provider != "anthropic" || list[1].Name != "B" {
		t.Fatalf("successful rename = %+v", list)
	}
	if got := loadRawConfig().CustomModelName; got != "C" {
		t.Fatalf("selected profile reference = %q, want C after rename", got)
	}
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{OriginalName: "missing", Name: "D", Model: "m"}); err == nil {
		t.Fatal("want stale edit to fail instead of creating")
	}
}

func TestDeleteCustomModelClearsSelectedProfileReference(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{Name: "selected", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := updateRawConfig(func(cfg *StartSessionRequest) error {
		cfg.CustomModelName = "selected"
		cfg.Provider = "openai"
		cfg.Model = "m"
		cfg.BaseURL = "https://stale.invalid"
		cfg.APIKeyProtected = "stale"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteCustomModel("selected"); err != nil {
		t.Fatal(err)
	}
	raw := loadRawConfig()
	if raw.CustomModelName != "" || raw.Provider != "" || raw.Model != "" || raw.BaseURL != "" || raw.APIKeyProtected != "" {
		t.Fatalf("deleted selected profile left stale connection fields: %+v", raw)
	}
}

func TestSaveCustomModelSecretIntentAndRedaction(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})

	// Seed an opaque protected value directly. Keep/clear semantics must not depend
	// on this test platform having DPAPI available.
	if err := updateRawConfig(func(cfg *StartSessionRequest) error {
		cfg.CustomModels = []CustomModel{{
			Name: "secured", Provider: "openai", Model: "m", APIKeyProtected: "opaque-ciphertext",
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	list := app.ListCustomModels()
	if len(list) != 1 || !list[0].HasAPIKey || list[0].APIKey != "" || list[0].APIKeyProtected != "" {
		t.Fatalf("redacted list = %+v", list)
	}

	if _, err := app.SaveCustomModel(SaveCustomModelRequest{OriginalName: "secured", Name: "secured", Model: "m2"}); err != nil {
		t.Fatal(err)
	}
	raw := loadRawConfig().CustomModels
	if len(raw) != 1 || raw[0].APIKeyProtected != "opaque-ciphertext" {
		t.Fatalf("blank edit did not preserve ciphertext: %+v", raw)
	}

	list, err := app.SaveCustomModel(SaveCustomModelRequest{OriginalName: "secured", Name: "secured", Model: "m2", ClearAPIKey: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].HasAPIKey || loadRawConfig().CustomModels[0].APIKeyProtected != "" {
		t.Fatalf("clear key failed: list=%+v raw=%+v", list, loadRawConfig().CustomModels)
	}

	if _, ok := protectSecret("probe"); ok {
		list, err = app.SaveCustomModel(SaveCustomModelRequest{OriginalName: "secured", Name: "secured", Model: "m2", APIKey: "new-secret"})
		if err != nil {
			t.Fatal(err)
		}
		if !list[0].HasAPIKey || list[0].APIKey != "" || list[0].APIKeyProtected != "" {
			t.Fatalf("replacement response leaked key: %+v", list[0])
		}
		resolved, err := app.resolveCustomModel("secured")
		if err != nil || resolved.APIKey != "new-secret" {
			t.Fatalf("resolved = %+v, err=%v", resolved, err)
		}
	}

	path, err := desktopConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "new-secret") || strings.Contains(string(data), "originalName") || strings.Contains(string(data), "clearAPIKey") {
		t.Fatalf("request-only/plaintext fields reached desktop.json: %s", data)
	}
}

func TestListCustomModelsLegacyProviderDefaultsToOpenAI(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	if err := updateRawConfig(func(cfg *StartSessionRequest) error {
		cfg.CustomModels = []CustomModel{{Name: "legacy", Model: "m"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := New(&recordingSink{})
	list := app.ListCustomModels()
	if len(list) != 1 || list[0].Provider != "openai" {
		t.Fatalf("legacy list = %+v", list)
	}
}

func TestLoadConfigDoesNotExposeCustomModelStorage(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	if err := updateRawConfig(func(cfg *StartSessionRequest) error {
		cfg.CustomModels = []CustomModel{{Name: "secured", Model: "m", APIKey: "legacy-plaintext", APIKeyProtected: "ciphertext"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := New(&recordingSink{}).LoadConfig()
	if len(got.CustomModels) != 0 {
		t.Fatalf("LoadConfig exposed backend-owned custom models: %+v", got.CustomModels)
	}
	if got.APIKey != "" || got.AuthToken != "" || got.APIKeyProtected != "" || got.AuthTokenProtected != "" {
		t.Fatalf("LoadConfig exposed top-level credentials: %+v", got)
	}
}

func TestCustomModelPersistenceRequestDropsExpandedCredentials(t *testing.T) {
	original := StartSessionRequest{
		CWD: "workspace", CustomModelName: " local ", Provider: "malicious", Model: "wrong",
		BaseURL: "https://client.invalid", APIKey: "client-secret", AuthToken: "client-token",
		APIKeyProtected: "client-protected", AuthTokenProtected: "client-auth-protected",
	}
	resolved := StartSessionRequest{
		CWD: "workspace", CustomModelName: "local", Provider: "anthropic", Model: "claude",
		BaseURL: "https://resolved.invalid", APIKey: "resolved-secret",
	}
	got := customModelPersistenceRequest(original, resolved)
	if got.CustomModelName != "local" || got.Provider != "anthropic" || got.Model != "claude" {
		t.Fatalf("identity = %+v, want canonical resolved profile", got)
	}
	if got.BaseURL != "" || got.APIKey != "" || got.AuthToken != "" || got.APIKeyProtected != "" || got.AuthTokenProtected != "" {
		t.Fatalf("persistence request retained expanded credentials: %+v", got)
	}
}

func TestResolveCustomModelRequestDoesNotExposeOrInheritCredentials(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("ANTHROPIC_BASE_URL", "https://env.invalid")
	t.Setenv("ANTHROPIC_API_KEY", "env-secret")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token")
	app := New(&recordingSink{})
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{Name: "local", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	req, err := app.resolveCustomModelRequest(StartSessionRequest{CWD: t.TempDir(), CustomModelName: " local "})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := buildConfig(req)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "openai" || cfg.Model != "m" || cfg.BaseURL != "" || cfg.APIKey != "" || cfg.AuthToken != "" {
		t.Fatalf("custom config inherited environment credentials: %+v", cfg)
	}
}

func TestSwitchModelGuards(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	if _, err := app.SwitchModel("platform", "   "); err == nil {
		t.Fatal("want error for empty model name")
	}
	wantNoSession := func(err error, what string) {
		t.Helper()
		var pe *protocol.Error
		if !errors.As(err, &pe) || pe.Code != protocol.ErrCodeNoSession {
			t.Fatalf("%s = %v, want protocol error with code %q", what, err, protocol.ErrCodeNoSession)
		}
	}
	_, err := app.SwitchModel("platform", "glm-4.6")
	wantNoSession(err, "SwitchModel without session")
	_, err = app.SwitchModel("custom", "本地 Ollama")
	wantNoSession(err, "SwitchModel custom without session")
}

// TestSaveCustomModelReappliesLiveConnection covers the fix: editing the custom
// model the live session is currently running must rebuild that session against
// the new connection immediately, so the change takes effect without a manual
// re-select in the model picker.
func TestSaveCustomModelReappliesLiveConnection(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})

	if _, err := app.SaveCustomModel(SaveCustomModelRequest{
		Name: "M", Provider: "openai", Model: "old-model", BaseURL: "http://old.invalid/v1",
	}); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	if _, err := app.StartSession(StartSessionRequest{CWD: t.TempDir(), CustomModelName: "M"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer app.CloseSession()

	app.mu.Lock()
	live := app.liveConfig
	passport := app.livePassport
	app.mu.Unlock()
	if passport || live.Model != "old-model" || live.BaseURL != "http://old.invalid/v1" {
		t.Fatalf("precondition: live custom connection = %+v passport=%v", live, passport)
	}

	// Edit the same model's connection; the live session must adopt it right away.
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{
		OriginalName: "M", Name: "M", Provider: "openai", Model: "new-model", BaseURL: "http://new.invalid/v1",
	}); err != nil {
		t.Fatalf("edit save: %v", err)
	}
	app.mu.Lock()
	live = app.liveConfig
	app.mu.Unlock()
	if live.Model != "new-model" || live.BaseURL != "http://new.invalid/v1" {
		t.Fatalf("live connection not re-applied after edit: model=%q url=%q", live.Model, live.BaseURL)
	}
}

// TestSaveCustomModelNoLiveSessionDoesNotRebuild guards the guard: with no live
// session, saving must simply persist (never reach the no-session rebuild path).
func TestSaveCustomModelNoLiveSessionDoesNotRebuild(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{Name: "M", Model: "m"}); err != nil {
		t.Fatalf("save without session: %v", err)
	}
}

func TestCustomModelStoredJSONShape(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{Name: "plain", Provider: "anthropic", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	path, err := desktopConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	models, ok := decoded["customModels"].([]any)
	if !ok || len(models) != 1 {
		t.Fatalf("customModels JSON = %#v", decoded["customModels"])
	}
	entry := models[0].(map[string]any)
	if entry["provider"] != "anthropic" || entry["name"] != "plain" {
		t.Fatalf("stored entry = %#v", entry)
	}
	for _, forbidden := range []string{"apiKey", "hasAPIKey", "originalName", "clearAPIKey"} {
		if _, exists := entry[forbidden]; exists {
			t.Fatalf("stored entry contains %s: %#v", forbidden, entry)
		}
	}
}
