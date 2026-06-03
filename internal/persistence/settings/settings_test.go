package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func userConfigPath(dir string) string {
	return filepath.Join(dir, AppDirName, UserFileName)
}

func TestLoadMissingFilesReturnsEmpty(t *testing.T) {
	t.Parallel()

	resolved, err := Load(LoadOptions{CWD: t.TempDir(), UserConfigDir: t.TempDir()})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resolved.Config != (Config{}) || resolved.ProjectPath != "" || resolved.UserPath != "" {
		t.Fatalf("resolved = %#v, want empty", resolved)
	}
}

func TestLoadUserLevel(t *testing.T) {
	t.Parallel()

	userDir := t.TempDir()
	writeFile(t, userConfigPath(userDir), `
model = "claude-user"
base_url = "https://user.example"
max_tokens = 4096
telemetry = "jsonl"
`)
	resolved, err := Load(LoadOptions{CWD: t.TempDir(), UserConfigDir: userDir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resolved.Config.Model != "claude-user" || resolved.Config.BaseURL != "https://user.example" || resolved.Config.Telemetry != "jsonl" {
		t.Fatalf("config = %#v, want user values", resolved.Config)
	}
	if resolved.Config.MaxTokens == nil || *resolved.Config.MaxTokens != 4096 {
		t.Fatalf("max tokens = %v, want 4096", resolved.Config.MaxTokens)
	}
	if resolved.UserPath != userConfigPath(userDir) {
		t.Fatalf("user path = %q", resolved.UserPath)
	}
}

func TestProjectOverridesUser(t *testing.T) {
	t.Parallel()

	userDir := t.TempDir()
	writeFile(t, userConfigPath(userDir), `
model = "claude-user"
telemetry = "jsonl"
`)
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, ProjectFileName), `
model = "claude-project"
`)

	resolved, err := Load(LoadOptions{CWD: projectDir, UserConfigDir: userDir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resolved.Config.Model != "claude-project" {
		t.Fatalf("model = %q, want project override", resolved.Config.Model)
	}
	if resolved.Config.Telemetry != "jsonl" {
		t.Fatalf("telemetry = %q, want user value to survive", resolved.Config.Telemetry)
	}
	if resolved.ProjectPath == "" {
		t.Fatalf("project path not recorded")
	}
}

func TestProjectWalksUp(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, ProjectFileName), `model = "claude-up"`)
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	resolved, err := Load(LoadOptions{CWD: nested})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resolved.Config.Model != "claude-up" {
		t.Fatalf("model = %q, want value from ancestor runcode.toml", resolved.Config.Model)
	}
}

func TestCredentialsOnlyFromUser(t *testing.T) {
	t.Parallel()

	userDir := t.TempDir()
	writeFile(t, userConfigPath(userDir), `
api_key = "user-key"
`)
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, ProjectFileName), `
api_key = "project-key"
auth_token = "project-token"
model = "claude-project"
`)

	resolved, err := Load(LoadOptions{CWD: projectDir, UserConfigDir: userDir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resolved.Config.APIKey != "user-key" {
		t.Fatalf("api key = %q, want user-key (project credentials must be dropped)", resolved.Config.APIKey)
	}
	if resolved.Config.AuthToken != "" {
		t.Fatalf("auth token = %q, want empty (project credentials must be dropped)", resolved.Config.AuthToken)
	}
	if resolved.Config.Model != "claude-project" {
		t.Fatalf("model = %q, want non-credential project value applied", resolved.Config.Model)
	}
}

func TestUnknownFieldsIgnored(t *testing.T) {
	t.Parallel()

	userDir := t.TempDir()
	writeFile(t, userConfigPath(userDir), `
model = "claude-user"
future_unknown_option = "whatever"
`)
	resolved, err := Load(LoadOptions{CWD: t.TempDir(), UserConfigDir: userDir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resolved.Config.Model != "claude-user" {
		t.Fatalf("model = %q, want parsed despite unknown field", resolved.Config.Model)
	}
}

func TestSyntaxErrorReported(t *testing.T) {
	t.Parallel()

	userDir := t.TempDir()
	writeFile(t, userConfigPath(userDir), "model = \"unterminated\n")
	_, err := Load(LoadOptions{CWD: t.TempDir(), UserConfigDir: userDir})
	if err == nil {
		t.Fatal("want parse error for malformed TOML")
	}
}

func TestLeadingBOMTolerated(t *testing.T) {
	t.Parallel()

	userDir := t.TempDir()
	path := userConfigPath(userDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`model = "claude-bom"`)...)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	resolved, err := Load(LoadOptions{CWD: t.TempDir(), UserConfigDir: userDir})
	if err != nil {
		t.Fatalf("load with BOM: %v", err)
	}
	if resolved.Config.Model != "claude-bom" {
		t.Fatalf("model = %q, want value parsed despite BOM", resolved.Config.Model)
	}
}

func TestMaxTokensPointerDistinguishesZero(t *testing.T) {
	t.Parallel()

	userDir := t.TempDir()
	writeFile(t, userConfigPath(userDir), `max_tokens = 0`)
	resolved, err := Load(LoadOptions{CWD: t.TempDir(), UserConfigDir: userDir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resolved.Config.MaxTokens == nil || *resolved.Config.MaxTokens != 0 {
		t.Fatalf("max tokens = %v, want explicit 0 (non-nil)", resolved.Config.MaxTokens)
	}
}
