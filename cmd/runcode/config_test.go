package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// isolateConfigEnv clears every flag-backing env var and points the per-user
// config directory at an empty temp dir so tests only see the files they create.
func isolateConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"ANTHROPIC_MODEL", "ANTHROPIC_MAX_TOKENS", "ANTHROPIC_BASE_URL",
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "RUNCODE_PROVIDER",
		"RUNCODE_CWD", "RUNCODE_TELEMETRY", "RUNCODE_PERMISSION_MODE",
		"RUNCODE_TRANSCRIPT", "RUNCODE_SESSION_ID", "RUNCODE_MAX_HISTORY_MESSAGES",
		"RUNCODE_SESSION_BACKEND",
	} {
		t.Setenv(key, "")
	}
	emptyUser := t.TempDir()
	t.Setenv("AppData", emptyUser)         // Windows os.UserConfigDir
	t.Setenv("XDG_CONFIG_HOME", emptyUser) // Linux os.UserConfigDir
	t.Setenv("HOME", emptyUser)            // macOS os.UserConfigDir
}

func writeProjectConfig(t *testing.T, dir string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "runcode.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
	}
}

func configFlagsCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	addChatConfigFlags(cmd)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	return cmd
}

func TestResolveChatConfigUsesProjectFile(t *testing.T) {
	isolateConfigEnv(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "model = \"claude-file\"\nbase_url = \"https://file.example\"\npermission_mode = \"interactive\"\nmax_tokens = 4096\n")

	cfg, resolved, err := resolveChatConfig(configFlagsCmd(t, "--cwd", dir))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.Model != "claude-file" || cfg.BaseURL != "https://file.example" || cfg.PermissionMode != "interactive" || cfg.MaxTokens != 4096 {
		t.Fatalf("cfg = %#v, want project-file values", cfg)
	}
	if resolved.ProjectPath == "" {
		t.Fatalf("project path not reported")
	}
}

func TestFlagOverridesProjectFile(t *testing.T) {
	isolateConfigEnv(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "model = \"claude-file\"\n")

	cfg, _, err := resolveChatConfig(configFlagsCmd(t, "--cwd", dir, "--model", "claude-flag"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.Model != "claude-flag" {
		t.Fatalf("model = %q, want flag to win over file", cfg.Model)
	}
}

func TestEnvOverridesProjectFile(t *testing.T) {
	isolateConfigEnv(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "model = \"claude-file\"\n")
	t.Setenv("ANTHROPIC_MODEL", "claude-env")

	cfg, _, err := resolveChatConfig(configFlagsCmd(t, "--cwd", dir))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.Model != "claude-env" {
		t.Fatalf("model = %q, want env to win over file", cfg.Model)
	}
}

func TestProjectFileCredentialsIgnored(t *testing.T) {
	isolateConfigEnv(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "api_key = \"project-secret\"\nauth_token = \"project-token\"\nmodel = \"claude-file\"\n")

	cfg, _, err := resolveChatConfig(configFlagsCmd(t, "--cwd", dir))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.APIKey != "" || cfg.AuthToken != "" {
		t.Fatalf("credentials = %q/%q, want empty (project file must not supply credentials)", cfg.APIKey, cfg.AuthToken)
	}
	if cfg.Model != "claude-file" {
		t.Fatalf("model = %q, want non-credential project value applied", cfg.Model)
	}
}

func TestInvalidFilePermissionModeErrors(t *testing.T) {
	isolateConfigEnv(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "permission_mode = \"bogus\"\n")

	if _, _, err := resolveChatConfig(configFlagsCmd(t, "--cwd", dir)); err == nil {
		t.Fatal("want error for invalid permission_mode in config file")
	}
}

func TestConfigCommandRedactsCredentials(t *testing.T) {
	isolateConfigEnv(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "model = \"claude-file\"\n")

	cmd := configCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--cwd", dir, "--api-key", "supersecret"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config command: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "api-key set") {
		t.Fatalf("output = %q, want 'api-key set'", text)
	}
	if strings.Contains(text, "supersecret") {
		t.Fatalf("output leaked credential: %q", text)
	}
	if !strings.Contains(text, "claude-file") {
		t.Fatalf("output = %q, want effective model", text)
	}
}
