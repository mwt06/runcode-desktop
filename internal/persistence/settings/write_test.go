package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func bptr(b bool) *bool { return &b }

func TestWriteUserMCPServersRoundTripPreservesOtherSettings(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, AppDirName, UserFileName)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed a config with settings that must survive the MCP write untouched.
	seed := "provider = \"openai\"\nbase_url = \"https://x/v1\"\n\n[mcp]\nallow_sampling = true\n\n[[hooks]]\nevent = \"PreToolUse\"\ncommand = [\"echo\", \"hi\"]\n"
	if err := os.WriteFile(cfgPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	servers := map[string]MCPServerConfig{
		"fs":     {Command: "npx", Args: []string{"-y", "server"}, Env: map[string]string{"TOKEN": "${T}"}},
		"remote": {Transport: "http", URL: "https://mcp.example.com", Enabled: bptr(false)},
	}
	if err := WriteUserMCPServers(dir, servers); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := Load(LoadOptions{UserConfigDir: dir, CWD: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if res.Config.Provider != "openai" || res.Config.BaseURL != "https://x/v1" {
		t.Fatalf("unrelated settings lost: %#v", res.Config)
	}
	if res.Config.MCP.AllowSampling == nil || !*res.Config.MCP.AllowSampling {
		t.Fatalf("mcp.allow_sampling lost: %#v", res.Config.MCP)
	}
	if len(res.Config.Hooks) != 1 || res.Config.Hooks[0].Event != "PreToolUse" {
		t.Fatalf("hooks lost: %#v", res.Config.Hooks)
	}
	fs, ok := res.Config.MCP.Servers["fs"]
	if !ok || fs.Command != "npx" || len(fs.Args) != 2 || fs.Env["TOKEN"] != "${T}" {
		t.Fatalf("fs server = %#v", fs)
	}
	remote, ok := res.Config.MCP.Servers["remote"]
	if !ok || remote.Transport != "http" || remote.URL != "https://mcp.example.com" || remote.Enabled == nil || *remote.Enabled {
		t.Fatalf("remote server = %#v", remote)
	}

	// Clearing removes the servers table but keeps other mcp keys.
	if err := WriteUserMCPServers(dir, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	res2, err := Load(LoadOptions{UserConfigDir: dir, CWD: dir})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(res2.Config.MCP.Servers) != 0 {
		t.Fatalf("servers not cleared: %#v", res2.Config.MCP.Servers)
	}
	if res2.Config.MCP.AllowSampling == nil || !*res2.Config.MCP.AllowSampling {
		t.Fatalf("allow_sampling lost after clear: %#v", res2.Config.MCP)
	}
}
