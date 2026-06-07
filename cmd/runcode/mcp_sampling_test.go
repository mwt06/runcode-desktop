package main

import "testing"

func TestResolveChatConfigAllowMCPSampling(t *testing.T) {
	isolateConfigEnv(t)
	t.Setenv("RUNCODE_ALLOW_MCP_SAMPLING", "")

	// Default off.
	cfg, _, err := resolveChatConfig(configFlagsCmd(t, "--cwd", t.TempDir()))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.AllowMCPSampling {
		t.Fatal("sampling should be off by default")
	}

	// Enabled by flag.
	cfg, _, err = resolveChatConfig(configFlagsCmd(t, "--cwd", t.TempDir(), "--allow-mcp-sampling"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !cfg.AllowMCPSampling {
		t.Fatal("--allow-mcp-sampling should enable sampling")
	}
}

func TestResolveChatConfigAllowMCPSamplingFromEnv(t *testing.T) {
	isolateConfigEnv(t)
	t.Setenv("RUNCODE_ALLOW_MCP_SAMPLING", "true")
	cfg, _, err := resolveChatConfig(configFlagsCmd(t, "--cwd", t.TempDir()))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !cfg.AllowMCPSampling {
		t.Fatal("RUNCODE_ALLOW_MCP_SAMPLING=true should enable sampling")
	}
}
