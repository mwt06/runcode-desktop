package main

import "testing"

// clearPriceEnv ensures the price env vars do not leak into the resolution.
func clearPriceEnv(t *testing.T) {
	t.Helper()
	t.Setenv("RUNCODE_INPUT_PRICE", "")
	t.Setenv("RUNCODE_OUTPUT_PRICE", "")
}

func TestResolveChatConfigAutoPricesKnownModel(t *testing.T) {
	isolateConfigEnv(t)
	clearPriceEnv(t)
	cfg, _, err := resolveChatConfig(configFlagsCmd(t, "--cwd", t.TempDir(), "--model", "claude-opus-4-8"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.PriceSource != "builtin" || cfg.InputPrice != 15 || cfg.OutputPrice != 75 {
		t.Fatalf("cfg pricing = %v/%g/%g, want builtin/15/75", cfg.PriceSource, cfg.InputPrice, cfg.OutputPrice)
	}
}

func TestResolveChatConfigUnknownModelUnpriced(t *testing.T) {
	isolateConfigEnv(t)
	clearPriceEnv(t)
	cfg, _, err := resolveChatConfig(configFlagsCmd(t, "--cwd", t.TempDir(), "--model", "qwen2.5-coder"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.PriceSource != "" || cfg.InputPrice != 0 || cfg.OutputPrice != 0 {
		t.Fatalf("cfg pricing = %q/%g/%g, want unpriced", cfg.PriceSource, cfg.InputPrice, cfg.OutputPrice)
	}
}

func TestResolveChatConfigExplicitPriceWins(t *testing.T) {
	isolateConfigEnv(t)
	clearPriceEnv(t)
	// An explicit price must win over the built-in table even for a known model.
	cfg, _, err := resolveChatConfig(configFlagsCmd(t, "--cwd", t.TempDir(), "--model", "claude-opus-4-8", "--input-price", "1.5"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.PriceSource != "explicit" || cfg.InputPrice != 1.5 {
		t.Fatalf("cfg pricing = %q/%g, want explicit/1.5", cfg.PriceSource, cfg.InputPrice)
	}
	// The unset output side stays 0 (the table is not consulted at all).
	if cfg.OutputPrice != 0 {
		t.Fatalf("output price = %g, want 0 (table not consulted when explicit)", cfg.OutputPrice)
	}
}

func TestResolveChatConfigExplicitZeroIsNotAutoPriced(t *testing.T) {
	isolateConfigEnv(t)
	clearPriceEnv(t)
	// Explicitly setting 0 (e.g. a free local endpoint) must not be auto-filled
	// from the table, even if the model name is known.
	cfg, _, err := resolveChatConfig(configFlagsCmd(t, "--cwd", t.TempDir(), "--model", "claude-opus-4-8", "--input-price", "0"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.PriceSource != "explicit" || cfg.InputPrice != 0 || cfg.OutputPrice != 0 {
		t.Fatalf("cfg pricing = %q/%g/%g, want explicit 0/0", cfg.PriceSource, cfg.InputPrice, cfg.OutputPrice)
	}
}
