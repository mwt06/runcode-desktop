package desktop

import (
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

func TestBuildConfigRequiresWorkspace(t *testing.T) {
	t.Parallel()
	if _, err := buildConfig(StartSessionRequest{Model: "m"}); err == nil {
		t.Fatal("want error when workspace is empty")
	}
}

func TestBuildConfigRequiresModel(t *testing.T) {
	t.Setenv("ANTHROPIC_MODEL", "")
	if _, err := buildConfig(StartSessionRequest{CWD: t.TempDir()}); err == nil {
		t.Fatal("want error when model is unset")
	}
}

func TestBuildConfigMaxTokensDefaultsGenerously(t *testing.T) {
	t.Parallel()
	// No requested value → 80k for every provider, so large file writes are not
	// truncated mid-JSON; an explicit value wins, which is the only way to stay under
	// an endpoint whose ceiling is lower than the default.
	for _, provider := range []string{"", "openai", "anthropic", "passport", "custom"} {
		cfg, err := buildConfig(StartSessionRequest{CWD: t.TempDir(), Model: "m", Provider: provider})
		if err != nil {
			t.Fatalf("buildConfig(%q): %v", provider, err)
		}
		if cfg.MaxTokens != desktopDefaultMaxTokens {
			t.Fatalf("%q MaxTokens = %d, want %d", provider, cfg.MaxTokens, desktopDefaultMaxTokens)
		}
	}
	cfg, err := buildConfig(StartSessionRequest{CWD: t.TempDir(), Model: "m", MaxTokens: 2048})
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.MaxTokens != 2048 {
		t.Fatalf("MaxTokens = %d, want explicit 2048", cfg.MaxTokens)
	}
}

func TestBuildConfigModelFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_MODEL", "claude-from-env")
	cfg, err := buildConfig(StartSessionRequest{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.Model != "claude-from-env" {
		t.Fatalf("model = %q, want env fallback", cfg.Model)
	}
	if cfg.PermissionMode != "safe" {
		t.Fatalf("permission mode = %q, want safe default", cfg.PermissionMode)
	}
	if !cfg.PersistSession {
		t.Fatal("expected session persistence on by default")
	}
}

func TestBuildConfigRejectsBadPermissionMode(t *testing.T) {
	t.Parallel()
	_, err := buildConfig(StartSessionRequest{CWD: t.TempDir(), Model: "m", PermissionMode: "yolo"})
	if err == nil {
		t.Fatal("want error for unsupported permission mode")
	}
}

func TestBuildConfigContextControl(t *testing.T) {
	t.Parallel()
	// The two context levers pass through verbatim; 0 leaves each off.
	cfg, err := buildConfig(StartSessionRequest{CWD: t.TempDir(), Model: "m", MaxContextTokens: 128000, MaxHistoryMessages: 40})
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.MaxContextTokens != 128000 {
		t.Fatalf("MaxContextTokens = %d, want 128000", cfg.MaxContextTokens)
	}
	if cfg.MaxHistoryMessages != 40 {
		t.Fatalf("MaxHistoryMessages = %d, want 40", cfg.MaxHistoryMessages)
	}
	// A negative from a stray form value is clamped to off, never sent as-is.
	cfg, err = buildConfig(StartSessionRequest{CWD: t.TempDir(), Model: "m", MaxContextTokens: -5})
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.MaxContextTokens != 0 {
		t.Fatalf("negative MaxContextTokens = %d, want clamped to 0", cfg.MaxContextTokens)
	}
}

func TestBuildConfigThinkingEffort(t *testing.T) {
	t.Parallel()
	// Absent → thinking off (no reasoning_effort sent).
	cfg, err := buildConfig(StartSessionRequest{CWD: t.TempDir(), Model: "m"})
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.Thinking.Enabled() {
		t.Fatalf("thinking should default to off, got %+v", cfg.Thinking)
	}
	// A valid strength maps into the provider-native thinking config.
	cfg, err = buildConfig(StartSessionRequest{CWD: t.TempDir(), Model: "m", ThinkingEffort: "High"})
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.Thinking.Effort != llm.ThinkingHigh {
		t.Fatalf("thinking effort = %q, want high", cfg.Thinking.Effort)
	}
	// An unknown strength is rejected rather than silently ignored.
	if _, err := buildConfig(StartSessionRequest{CWD: t.TempDir(), Model: "m", ThinkingEffort: "turbo"}); err == nil {
		t.Fatal("want error for unsupported thinking effort")
	}
}

func TestBuildConfigExplicitBaseURLDoesNotInheritEnvironmentCredentials(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://env.invalid")
	t.Setenv("ANTHROPIC_API_KEY", "env-secret")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token")
	cfg, err := buildConfig(StartSessionRequest{
		CWD: t.TempDir(), Provider: "openai", Model: "m", BaseURL: "https://explicit.invalid/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://explicit.invalid/v1" || cfg.APIKey != "" || cfg.AuthToken != "" {
		t.Fatalf("explicit endpoint inherited environment credentials: %+v", cfg)
	}
}

func TestBuildConfigRequestOverridesEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_MODEL", "env-model")
	t.Setenv("RUNCODE_PROVIDER", "anthropic")
	cfg, err := buildConfig(StartSessionRequest{
		CWD:            t.TempDir(),
		Model:          "req-model",
		Provider:       "openai",
		PermissionMode: "interactive",
	})
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.Model != "req-model" {
		t.Fatalf("model = %q, want request override", cfg.Model)
	}
	if cfg.Provider != "openai" {
		t.Fatalf("provider = %q, want request override", cfg.Provider)
	}
	if cfg.PermissionMode != "interactive" {
		t.Fatalf("mode = %q, want interactive", cfg.PermissionMode)
	}
}

// One output budget for every connection: 80k. Overshooting an endpoint's ceiling is
// a hard 400 rather than a clamp, so a model capped lower needs an explicit value —
// that escape hatch is what the second half of this test guards.
func TestMaxTokensDefaultsTo80k(t *testing.T) {
	t.Parallel()

	if got := maxTokensOrDefault(0); got != 81920 {
		t.Fatalf("default = %d, want 81920", got)
	}
	// An explicit value always wins, including one below the default: a model with a
	// lower ceiling has no other way to be usable.
	if got := maxTokensOrDefault(4096); got != 4096 {
		t.Fatalf("explicit request = %d, want it honored", got)
	}
	// A negative or zero request is "unset", not a budget of its own.
	if got := maxTokensOrDefault(-1); got != 81920 {
		t.Fatalf("negative request = %d, want the default", got)
	}
}
