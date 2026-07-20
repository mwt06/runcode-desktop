package desktop

import (
	"path/filepath"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.FromSlash(p))
	if err != nil {
		t.Fatalf("abs %q: %v", p, err)
	}
	return abs
}

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
	// No requested value → a generous default so large file writes are not
	// truncated; an explicit value wins.
	cfg, err := buildConfig(StartSessionRequest{CWD: t.TempDir(), Model: "m"})
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.MaxTokens != desktopDefaultMaxTokens {
		t.Fatalf("MaxTokens = %d, want default %d", cfg.MaxTokens, desktopDefaultMaxTokens)
	}
	cfg, err = buildConfig(StartSessionRequest{CWD: t.TempDir(), Model: "m", MaxTokens: 2048})
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
