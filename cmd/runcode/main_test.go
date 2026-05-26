package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestVersionCommandOutput(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	cmd := versionCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute version: %v", err)
	}
	text := out.String()
	for _, want := range []string{"runcode 0.1.0-alpha", "commit:", "built:", "go:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("version output missing %q: %q", want, text)
		}
	}
}

func TestChatCommandUsesArgsPrompt(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "hello", "world"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.prompt != "hello world" {
		t.Fatalf("prompt = %q, want hello world", runner.prompt)
	}
	if out.String() != "done\n" {
		t.Fatalf("stdout = %q, want done newline", out.String())
	}
}

func TestChatCommandUsesStdinPrompt(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test"})
	cmd.SetIn(strings.NewReader("hello from stdin\n"))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.prompt != "hello from stdin" {
		t.Fatalf("prompt = %q, want stdin text", runner.prompt)
	}
}

func TestChatCommandRejectsEmptyPrompt(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	cmd := newChatCmd(&fakeChatRunner{})
	cmd.SetArgs([]string{"--model", "claude-test"})
	cmd.SetIn(strings.NewReader(" \n\t"))

	if err := cmd.Execute(); !errors.Is(err, errEmptyPrompt) {
		t.Fatalf("err = %v, want empty prompt", err)
	}
}

func TestChatCommandRequiresModel(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	cmd := newChatCmd(&fakeChatRunner{})
	cmd.SetArgs([]string{"hello"})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("err = %v, want model required", err)
	}
}

func TestChatCommandRequiresCredential(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	cmd := newChatCmd(&fakeChatRunner{})
	cmd.SetArgs([]string{"--model", "claude-test", "hello"})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "api key or auth token") {
		t.Fatalf("err = %v, want credential required", err)
	}
}

func TestChatCommandReadsConfigFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_MODEL", "claude-env")
	t.Setenv("ANTHROPIC_BASE_URL", "https://example.invalid")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token")
	t.Setenv("ANTHROPIC_MAX_TOKENS", "123")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.Model != "claude-env" || runner.cfg.BaseURL != "https://example.invalid" || runner.cfg.AuthToken != "env-token" || runner.cfg.MaxTokens != 123 {
		t.Fatalf("unexpected config: %#v", runner.cfg)
	}
	if runner.cfg.APIKey != "" {
		t.Fatalf("api key = %q, want empty when auth token is set", runner.cfg.APIKey)
	}
}

func TestChatCommandFlagsOverrideEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_MODEL", "claude-env")
	t.Setenv("ANTHROPIC_BASE_URL", "https://env.invalid")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token")
	t.Setenv("ANTHROPIC_MAX_TOKENS", "123")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{
		"--model", "claude-flag",
		"--base-url", "https://flag.invalid",
		"--api-key", "flag-key",
		"--max-tokens", "456",
		"hello",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.Model != "claude-flag" || runner.cfg.BaseURL != "https://flag.invalid" || runner.cfg.APIKey != "flag-key" || runner.cfg.MaxTokens != 456 {
		t.Fatalf("unexpected config: %#v", runner.cfg)
	}
	if runner.cfg.AuthToken != "" {
		t.Fatalf("auth token = %q, want empty when api-key flag is set", runner.cfg.AuthToken)
	}
}

func TestChatCommandAuthTokenFlagOverridesAPIKeyFlag(t *testing.T) {
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "--api-key", "key", "--auth-token", "token", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.AuthToken != "token" || runner.cfg.APIKey != "" {
		t.Fatalf("unexpected credentials: %#v", runner.cfg)
	}
}

func TestChatCommandDefaultsTelemetryOff(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.Telemetry != "off" {
		t.Fatalf("telemetry = %q, want off", runner.cfg.Telemetry)
	}
}

func TestChatCommandReadsTelemetryFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	t.Setenv("RUNCODE_TELEMETRY", "stderr-jsonl")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.Telemetry != "jsonl" {
		t.Fatalf("telemetry = %q, want jsonl", runner.cfg.Telemetry)
	}
}

func TestChatCommandTelemetryFlagOverridesEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	t.Setenv("RUNCODE_TELEMETRY", "jsonl")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "--telemetry", "off", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.Telemetry != "off" {
		t.Fatalf("telemetry = %q, want off", runner.cfg.Telemetry)
	}
}

func TestChatCommandRejectsUnsupportedTelemetryMode(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	cmd := newChatCmd(&fakeChatRunner{})
	cmd.SetArgs([]string{"--model", "claude-test", "--telemetry", "remote", "hello"})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unsupported telemetry mode") {
		t.Fatalf("err = %v, want unsupported telemetry mode", err)
	}
}

func TestChatCommandPropagatesRunnerError(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	expected := errors.New("runner failed")
	cmd := newChatCmd(&fakeChatRunner{err: expected})
	cmd.SetArgs([]string{"--model", "claude-test", "hello"})

	if err := cmd.Execute(); !errors.Is(err, expected) {
		t.Fatalf("err = %v, want runner error", err)
	}
}

type fakeChatRunner struct {
	cfg    chatConfig
	prompt string
	text   string
	err    error
}

func (r *fakeChatRunner) Run(_ context.Context, cfg chatConfig, prompt string) (string, error) {
	r.cfg = cfg
	r.prompt = prompt
	return r.text, r.err
}
