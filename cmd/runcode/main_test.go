package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestChatCommandReadsMaxHistoryMessagesFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_MODEL", "claude-env")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token")
	t.Setenv("RUNCODE_MAX_HISTORY_MESSAGES", "20")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.MaxHistoryMessages != 20 {
		t.Fatalf("MaxHistoryMessages = %d, want 20", runner.cfg.MaxHistoryMessages)
	}
}

func TestChatCommandMaxHistoryMessagesFlagOverridesEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_MODEL", "claude-env")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token")
	t.Setenv("RUNCODE_MAX_HISTORY_MESSAGES", "20")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--max-history-messages", "5", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.MaxHistoryMessages != 5 {
		t.Fatalf("MaxHistoryMessages = %d, want 5", runner.cfg.MaxHistoryMessages)
	}
}

func TestChatCommandRejectsInvalidMaxHistoryMessages(t *testing.T) {
	t.Setenv("ANTHROPIC_MODEL", "claude-env")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token")
	t.Setenv("RUNCODE_MAX_HISTORY_MESSAGES", "not-a-number")
	cmd := newChatCmd(&fakeChatRunner{})
	cmd.SetArgs([]string{"hello"})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "RUNCODE_MAX_HISTORY_MESSAGES") {
		t.Fatalf("err = %v, want parse error for RUNCODE_MAX_HISTORY_MESSAGES", err)
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

func TestChatCommandDefaultsTranscriptOff(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.Transcript != "off" {
		t.Fatalf("transcript = %q, want off", runner.cfg.Transcript)
	}
}

func TestChatCommandReadsTranscriptFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	t.Setenv("RUNCODE_TRANSCRIPT", "jsonl")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.Transcript != "jsonl" {
		t.Fatalf("transcript = %q, want jsonl", runner.cfg.Transcript)
	}
}

func TestChatCommandTranscriptFlagOverridesEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	t.Setenv("RUNCODE_TRANSCRIPT", "jsonl")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "--transcript", "off", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.Transcript != "off" {
		t.Fatalf("transcript = %q, want off", runner.cfg.Transcript)
	}
}

func TestChatCommandRejectsUnsupportedTranscriptMode(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	cmd := newChatCmd(&fakeChatRunner{})
	cmd.SetArgs([]string{"--model", "claude-test", "--transcript", "sqlite", "hello"})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unsupported transcript mode") {
		t.Fatalf("err = %v, want unsupported transcript mode", err)
	}
}

func TestChatCommandReadsSessionIDFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	t.Setenv("RUNCODE_SESSION_ID", "sess_env")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.SessionID != "sess_env" {
		t.Fatalf("session id = %q, want sess_env", runner.cfg.SessionID)
	}
}

func TestChatCommandSessionIDFlagOverridesEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	t.Setenv("RUNCODE_SESSION_ID", "sess_env")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "--session-id", "sess_flag", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.SessionID != "sess_flag" {
		t.Fatalf("session id = %q, want sess_flag", runner.cfg.SessionID)
	}
}

func TestChatCommandRejectsInvalidSessionID(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	cmd := newChatCmd(&fakeChatRunner{})
	cmd.SetArgs([]string{"--model", "claude-test", "--session-id", "../bad", "hello"})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "invalid session id") {
		t.Fatalf("err = %v, want invalid session id", err)
	}
}

func TestChatCommandDefaultsPermissionModeSafe(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.PermissionMode != "safe" {
		t.Fatalf("permission mode = %q, want safe", runner.cfg.PermissionMode)
	}
}

func TestChatCommandReadsPermissionModeFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	t.Setenv("RUNCODE_PERMISSION_MODE", "interactive")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.PermissionMode != "interactive" {
		t.Fatalf("permission mode = %q, want interactive", runner.cfg.PermissionMode)
	}
}

func TestChatCommandPermissionModeFlagOverridesEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	t.Setenv("RUNCODE_PERMISSION_MODE", "safe")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "--permission-mode", "confirm", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.cfg.PermissionMode != "interactive" {
		t.Fatalf("permission mode = %q, want interactive", runner.cfg.PermissionMode)
	}
}

func TestChatCommandLoopReadsPromptsUntilExit(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "--loop"})
	cmd.SetIn(strings.NewReader("one\ntwo\n/exit\n"))
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat loop: %v", err)
	}
	if got, want := runner.prompts, []string{"one", "two"}; !sameStringSlices(got, want) {
		t.Fatalf("prompts = %#v, want %#v", got, want)
	}
	if out.String() != "done\ndone\n" {
		t.Fatalf("stdout = %q, want two responses", out.String())
	}
	if !strings.Contains(errOut.String(), "> ") {
		t.Fatalf("stderr missing prompt marker: %q", errOut.String())
	}
}

func TestChatCommandLoopClearsHistory(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "--loop"})
	cmd.SetIn(strings.NewReader("one\n/clear\ntwo\n/exit\n"))
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat loop: %v", err)
	}
	if got, want := runner.prompts, []string{"one", "two"}; !sameStringSlices(got, want) {
		t.Fatalf("prompts = %#v, want %#v", got, want)
	}
	if runner.resetCount != 1 {
		t.Fatalf("reset count = %d, want 1", runner.resetCount)
	}
	if out.String() != "done\ndone\n" {
		t.Fatalf("stdout = %q, want two responses", out.String())
	}
	if !strings.Contains(errOut.String(), "history cleared") {
		t.Fatalf("stderr missing clear confirmation: %q", errOut.String())
	}
}

func TestChatCommandLoopUsesArgsAsFirstPrompt(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "--loop", "first prompt"})
	cmd.SetIn(strings.NewReader("second\nquit\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat loop: %v", err)
	}
	if got, want := runner.prompts, []string{"first prompt", "second"}; !sameStringSlices(got, want) {
		t.Fatalf("prompts = %#v, want %#v", got, want)
	}
	if out.String() != "done\ndone\n" {
		t.Fatalf("stdout = %q, want two responses", out.String())
	}
}

func TestChatCommandLoopSkipsEmptyLinesAndStopsOnEOF(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "--loop"})
	cmd.SetIn(strings.NewReader("\n  \nonly\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat loop: %v", err)
	}
	if got, want := runner.prompts, []string{"only"}; !sameStringSlices(got, want) {
		t.Fatalf("prompts = %#v, want %#v", got, want)
	}
	if out.String() != "done\n" {
		t.Fatalf("stdout = %q, want one response", out.String())
	}
}

func TestChatCommandLoopRunsFinalLineWithoutTrailingNewline(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "--loop"})
	cmd.SetIn(strings.NewReader("only"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat loop: %v", err)
	}
	if got, want := runner.prompts, []string{"only"}; !sameStringSlices(got, want) {
		t.Fatalf("prompts = %#v, want %#v", got, want)
	}
	if out.String() != "done\n" {
		t.Fatalf("stdout = %q, want one response", out.String())
	}
}

func TestChatCommandPassesRuntimeIOToRunner(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "hello"})
	cmd.SetIn(strings.NewReader("approval\n"))
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.runtime.In == nil || runner.runtime.Lines != nil || runner.runtime.Err != &errOut {
		t.Fatalf("runtime IO was not propagated: %#v", runner.runtime)
	}
}

func TestChatCommandPassesLineReaderForInteractiveMode(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	runner := &fakeChatRunner{text: "done"}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "--permission-mode", "interactive", "hello"})
	cmd.SetIn(strings.NewReader("approval\n"))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if runner.runtime.Lines == nil {
		t.Fatal("interactive runtime missing line reader")
	}
}

func TestChatCommandRejectsUnsupportedPermissionMode(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	cmd := newChatCmd(&fakeChatRunner{})
	cmd.SetArgs([]string{"--model", "claude-test", "--permission-mode", "allow-all", "hello"})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unsupported permission mode") {
		t.Fatalf("err = %v, want unsupported permission mode", err)
	}
}

func TestTranscriptRecorderCreatesWorkspaceFile(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	recorder, sessionID, err := transcriptRecorder(chatConfig{CWD: workspace, Transcript: "jsonl", SessionID: "sess_test"})
	if err != nil {
		t.Fatalf("transcriptRecorder: %v", err)
	}
	defer recorder.Close(context.Background())
	if sessionID != "sess_test" {
		t.Fatalf("session id = %q, want sess_test", sessionID)
	}
	path := filepath.Join(workspace, ".runcode", "transcripts", "sess_test.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat transcript file: %v", err)
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
	cfg        chatConfig
	runtime    chatIO
	prompt     string
	prompts    []string
	text       string
	err        error
	resetCount int
}

func (r *fakeChatRunner) Run(_ context.Context, cfg chatConfig, runtime chatIO, prompt string) (string, error) {
	r.cfg = cfg
	r.runtime = runtime
	r.prompt = prompt
	r.prompts = append(r.prompts, prompt)
	return r.text, r.err
}

func (r *fakeChatRunner) Reset(context.Context) error {
	r.resetCount++
	return nil
}

func sameStringSlices(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
