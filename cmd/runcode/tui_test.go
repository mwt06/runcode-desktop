package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
	"github.com/wt68/runcode/internal/ui"
)

type fakeTuiRunner struct {
	cfg      chatConfig
	runCount int
	err      error
}

func (r *fakeTuiRunner) Run(_ context.Context, cfg chatConfig) error {
	r.cfg = cfg
	r.runCount++
	return r.err
}

func TestTuiCommandReadsConfigFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_MODEL", "claude-env")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token")
	t.Setenv("RUNCODE_CWD", ".")
	runner := &fakeTuiRunner{}
	cmd := newTuiCmd(runner)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute tui: %v", err)
	}
	if runner.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", runner.runCount)
	}
	if runner.cfg.Model != "claude-env" || runner.cfg.AuthToken != "env-token" || runner.cfg.PermissionMode != "safe" {
		t.Fatalf("unexpected config: %#v", runner.cfg)
	}
}

func TestTuiCommandFlagsOverrideEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_MODEL", "claude-env")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token")
	t.Setenv("RUNCODE_MAX_HISTORY_MESSAGES", "20")
	runner := &fakeTuiRunner{}
	cmd := newTuiCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-flag", "--api-key", "flag-key", "--max-history-messages", "5"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute tui: %v", err)
	}
	if runner.cfg.Model != "claude-flag" || runner.cfg.APIKey != "flag-key" || runner.cfg.AuthToken != "" || runner.cfg.MaxHistoryMessages != 5 {
		t.Fatalf("unexpected config: %#v", runner.cfg)
	}
}

func TestTuiCommandRequiresModel(t *testing.T) {
	t.Setenv("ANTHROPIC_MODEL", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	cmd := newTuiCmd(&fakeTuiRunner{})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("err = %v, want model required", err)
	}
}

func TestTuiCommandRequiresCredential(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	cmd := newTuiCmd(&fakeTuiRunner{})
	cmd.SetArgs([]string{"--model", "claude-test"})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "api key or auth token") {
		t.Fatalf("err = %v, want credential required", err)
	}
}

func TestTuiCommandAcceptsInteractivePermissionMode(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	runner := &fakeTuiRunner{}
	cmd := newTuiCmd(runner)
	cmd.SetArgs([]string{"--model", "claude-test", "--permission-mode", "interactive"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute tui: %v", err)
	}
	if runner.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", runner.runCount)
	}
	if runner.cfg.PermissionMode != "interactive" {
		t.Fatalf("permission mode = %q, want interactive", runner.cfg.PermissionMode)
	}
}

func TestTuiCommandRejectsArgs(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	cmd := newTuiCmd(&fakeTuiRunner{})
	cmd.SetArgs([]string{"--model", "claude-test", "hello"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected args error")
	}
}

func TestTuiCommandDoesNotAcceptLoopFlag(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	cmd := newTuiCmd(&fakeTuiRunner{})
	cmd.SetArgs([]string{"--model", "claude-test", "--loop"})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("err = %v, want unknown flag", err)
	}
}

func TestTuiCommandPropagatesRunnerError(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")
	expected := errors.New("tui failed")
	cmd := newTuiCmd(&fakeTuiRunner{err: expected})
	cmd.SetArgs([]string{"--model", "claude-test"})

	if err := cmd.Execute(); !errors.Is(err, expected) {
		t.Fatalf("err = %v, want runner error", err)
	}
}

func TestBridgeTuiToolEventsForwardsEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	toolEvents := make(chan tool.Event, 1)
	events := make(chan tea.Msg, 1)
	go bridgeTuiToolEvents(ctx, toolEvents, events)

	toolEvents <- tool.Event{Type: tool.EventTypeStarted, ToolName: "Read", ToolUseID: "toolu_123"}

	select {
	case msg := <-events:
		if reflect.TypeOf(msg) != reflect.TypeOf(ui.ToolEvent(tool.Event{})) {
			t.Fatalf("unexpected msg type: %T", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for forwarded event")
	}
}
