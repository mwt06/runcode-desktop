package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/internal/repl"
	"github.com/wt68/runcode/internal/ui"
	"github.com/wt68/runcode/pkg/llm"
)

type tuiRunner interface {
	Run(ctx context.Context, cfg chatConfig) error
}

type defaultTuiRunner struct{}

func tuiCmd() *cobra.Command {
	return newTuiCmd(&defaultTuiRunner{})
}

func newTuiCmd(runner tuiRunner) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "tui",
		Short:        "Run the runcode terminal UI",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := chatConfigFromCommand(cmd)
			if err != nil {
				return err
			}
			if cfg.PermissionMode != "safe" {
				return errors.New("runcode tui MVP supports permission-mode=safe only; interactive TUI approvals are deferred")
			}
			return runner.Run(cmd.Context(), cfg)
		},
	}
	addChatConfigFlags(cmd)
	return cmd
}

func (r *defaultTuiRunner) Run(ctx context.Context, cfg chatConfig) error {
	service, err := newTuiSessionService(cfg)
	if err != nil {
		return err
	}
	defer service.Close(ctx)

	model := ui.New(service)
	events := model.Events()
	service.onDelta = func(delta string) { events <- ui.AssistantDelta(delta) }

	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err = program.Run()
	return err
}

type tuiSessionService struct {
	cfg       chatConfig
	session   *repl.Session
	resources sessionResources
	onDelta   func(string)
	closed    bool
}

func newTuiSessionService(cfg chatConfig) (*tuiSessionService, error) {
	service := &tuiSessionService{cfg: cfg}
	session, resources, err := newSessionForConfig(cfg, sessionFactoryOptions{
		Runtime:          chatIO{Err: io.Discard, Out: nil},
		TelemetryRuntime: chatIO{Err: os.Stderr},
		StreamDelta: func(delta string) {
			if service.onDelta != nil {
				service.onDelta(delta)
			}
		},
		Permissions: permissions.NewService(permissions.Options{Mode: "safe"}),
	})
	if err != nil {
		return nil, err
	}
	service.session = session
	service.resources = resources
	return service, nil
}

func (s *tuiSessionService) RunTurn(ctx context.Context, userText string) (ui.TurnResult, error) {
	result, err := s.session.RunTurn(ctx, userText)
	if err != nil {
		return ui.TurnResult{}, err
	}
	return ui.TurnResult{
		Text:            llm.TextContent(result.FinalAssistant),
		StopReason:      string(result.FinalStopReason),
		Iterations:      result.Iterations,
		ToolResultCount: len(result.ToolResults),
		InputTokens:     usageInputTokens(result.FinalUsage),
		OutputTokens:    usageOutputTokens(result.FinalUsage),
	}, nil
}

func (s *tuiSessionService) Reset(context.Context) error {
	s.session.ResetHistory()
	return nil
}

func (s *tuiSessionService) Close(ctx context.Context) error {
	if s.closed {
		return nil
	}
	s.closed = true
	return closeRecorders(ctx, s.resources.Telemetry, s.resources.Transcript)
}

func (s *tuiSessionService) Status() ui.Status {
	return ui.Status{
		Model:          s.cfg.Model,
		CWD:            s.cfg.CWD,
		PermissionMode: s.cfg.PermissionMode,
		Transcript:     s.cfg.Transcript,
		SessionID:      s.resources.SessionID,
	}
}

func usageInputTokens(usage *llm.Usage) int {
	if usage == nil {
		return 0
	}
	return usage.InputTokens
}

func usageOutputTokens(usage *llm.Usage) int {
	if usage == nil {
		return 0
	}
	return usage.OutputTokens
}

func formatTuiTurnSummary(result ui.TurnResult) string {
	if result.Iterations <= 1 && result.ToolResultCount == 0 {
		return ""
	}
	return fmt.Sprintf("done · %d iterations · %d tool results", result.Iterations, result.ToolResultCount)
}
