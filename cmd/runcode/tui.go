package main

import (
	"context"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/internal/repl"
	"github.com/wt68/runcode/internal/ui"
	"github.com/wt68/runcode/pkg/llm"
	"github.com/wt68/runcode/pkg/tool"
)

type tuiRunner interface {
	Run(ctx context.Context, cfg chatConfig) error
}

type defaultTuiRunner struct{}

const tuiToolEventBufferSize = 256

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
	if service.approver != nil {
		service.approver.SetEvents(events)
	}
	bridgeCtx, stopBridge := context.WithCancel(ctx)
	defer stopBridge()
	go bridgeTuiToolEvents(bridgeCtx, service.toolEvents, events)

	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err = program.Run()
	return err
}

type tuiSessionService struct {
	cfg        chatConfig
	session    *repl.Session
	resources  sessionResources
	onDelta    func(string)
	approver   *ui.Approver
	toolEvents chan tool.Event
	closed     bool
}

func newTuiSessionService(cfg chatConfig) (*tuiSessionService, error) {
	service := &tuiSessionService{cfg: cfg, toolEvents: make(chan tool.Event, tuiToolEventBufferSize)}
	permissionService := permissions.NewService(permissions.Options{Mode: "safe"})
	if cfg.PermissionMode == "interactive" {
		service.approver = ui.NewApprover(cfg.CWD)
		store, err := newAllowStore(cfg.CWD)
		if err != nil {
			return nil, err
		}
		permissionService = permissions.NewService(permissions.Options{
			Mode:              "interactive",
			ApprovalAvailable: true,
			Authorizer: permissions.InteractiveAuthorizer{
				Approver: service.approver,
				Store:    store,
			},
		})
	}
	session, resources, err := newSessionForConfig(cfg, sessionFactoryOptions{
		Runtime:          chatIO{Err: io.Discard, Out: nil},
		TelemetryRuntime: chatIO{Err: os.Stderr},
		StreamDelta: func(delta string) {
			if service.onDelta != nil {
				service.onDelta(delta)
			}
		},
		ToolEvents:  service.toolEvents,
		Permissions: permissionService,
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
	turn := ui.TurnResult{
		Text:            llm.TextContent(result.FinalAssistant),
		StopReason:      string(result.FinalStopReason),
		Iterations:      result.Iterations,
		ToolResultCount: len(result.ToolResults),
		InputTokens:     usageInputTokens(result.Usages),
		OutputTokens:    usageOutputTokens(result.Usages),
	}
	if result.ReasoningClassification != nil {
		turn.ReasoningScenario = string(result.ReasoningClassification.Scenario)
		turn.ReasoningConfidence = result.ReasoningClassification.Confidence
	}
	return turn, nil
}

func (s *tuiSessionService) Reset(context.Context) error {
	s.session.ResetHistory()
	return nil
}

func (s *tuiSessionService) Compact(ctx context.Context) (ui.CompactResult, error) {
	before, after, err := s.session.Compact(ctx)
	if err != nil {
		return ui.CompactResult{}, err
	}
	return ui.CompactResult{Before: before, After: after}, nil
}

func (s *tuiSessionService) Close(ctx context.Context) error {
	if s.closed {
		return nil
	}
	s.closed = true
	return closeRecorders(ctx, s.resources.Telemetry, s.resources.Transcript, s.resources.Sessions)
}

func (s *tuiSessionService) Status() ui.Status {
	return ui.Status{
		Model:              s.cfg.Model,
		CWD:                s.cfg.CWD,
		PermissionMode:     s.cfg.PermissionMode,
		Transcript:         s.cfg.Transcript,
		SessionID:          s.resources.SessionID,
		InputPricePerMTok:  s.cfg.InputPrice,
		OutputPricePerMTok: s.cfg.OutputPrice,
	}
}

func bridgeTuiToolEvents(ctx context.Context, toolEvents <-chan tool.Event, events chan<- tea.Msg) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-toolEvents:
			if !ok {
				return
			}
			select {
			case <-ctx.Done():
				return
			case events <- ui.ToolEvent(event):
			}
		}
	}
}

func usageInputTokens(usages []*llm.Usage) int {
	total := 0
	for _, usage := range usages {
		if usage != nil {
			total += usage.InputTokens
		}
	}
	return total
}

func usageOutputTokens(usages []*llm.Usage) int {
	total := 0
	for _, usage := range usages {
		if usage != nil {
			total += usage.OutputTokens
		}
	}
	return total
}

func formatTuiTurnSummary(result ui.TurnResult) string {
	if result.Iterations <= 1 && result.ToolResultCount == 0 {
		return ""
	}
	return fmt.Sprintf("done · %d iterations · %d tool results", result.Iterations, result.ToolResultCount)
}
