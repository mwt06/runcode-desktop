package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/internal/persistence/sessions"
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
			pick, _ := cmd.Flags().GetBool("pick")
			if pick && cfg.Resume == "" && !cfg.Continue {
				chosen, cancelled, err := pickSessionForTUI(cfg)
				if err != nil {
					return err
				}
				if cancelled {
					return nil
				}
				cfg.Resume = chosen // "" leaves a fresh session
			}
			return runner.Run(cmd.Context(), cfg)
		},
	}
	addChatConfigFlags(cmd)
	cmd.Flags().Bool("pick", false, "choose a saved session to resume at startup (interactive picker)")
	return cmd
}

// pickSessionForTUI lists the workspace's saved sessions and runs the interactive
// picker. With no saved sessions it returns an empty id (start fresh) without
// showing a picker. A returned cancelled=true means the user aborted and the TUI
// should not launch.
func pickSessionForTUI(cfg chatConfig) (chosenID string, cancelled bool, err error) {
	backend, err := sessions.OpenBackend(cfg.CWD, cfg.SessionBackend)
	if err != nil {
		return "", false, err
	}
	defer backend.Close(context.Background())
	infos, err := backend.List()
	if err != nil {
		return "", false, err
	}
	if len(infos) == 0 {
		return "", false, nil
	}
	summaries := make([]ui.SessionSummary, len(infos))
	for i, info := range infos {
		preview := info.LastUser
		if preview == "" {
			preview = info.FirstUser
		}
		summaries[i] = ui.SessionSummary{
			ID:      info.ID,
			When:    humanizeSince(time.Since(info.ModTime)),
			Turns:   info.Turns,
			Preview: preview,
		}
	}
	res, err := ui.PickSession(summaries)
	if err != nil {
		return "", false, err
	}
	return res.SessionID, res.Cancelled, nil
}

func (r *defaultTuiRunner) Run(ctx context.Context, cfg chatConfig) error {
	service, err := newTuiSessionService(cfg)
	if err != nil {
		return err
	}
	defer service.Close(ctx)

	customCommands, commandProblems := loadCustomCommands(cfg.CWD, userConfigDir())
	reportCommandProblems(os.Stderr, commandProblems)
	model := ui.New(service, ui.WithCustomCommands(uiCustomCommands(customCommands)))
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
	cfg         chatConfig
	session     *repl.Session
	resources   sessionResources
	onDelta     func(string)
	approver    *ui.Approver
	permissions *permissions.Service
	toolEvents  chan tool.Event
	closed      bool
}

func newTuiSessionService(cfg chatConfig) (*tuiSessionService, error) {
	service := &tuiSessionService{cfg: cfg, toolEvents: make(chan tool.Event, tuiToolEventBufferSize)}
	// Always build the approver and an interactive authorizer so /mode can switch
	// to interactive at runtime, even when starting in safe mode.
	service.approver = ui.NewApprover(cfg.CWD)
	store, err := newAllowStore(cfg.CWD)
	if err != nil {
		return nil, err
	}
	permissionService := permissions.NewService(permissions.Options{
		Mode:              cfg.PermissionMode,
		ApprovalAvailable: true,
		InteractiveAuthorizer: permissions.InteractiveAuthorizer{
			Approver: service.approver,
			Store:    store,
		},
	})
	service.permissions = permissionService
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
	text, images := parseImageAttachments(userText, s.cfg.CWD)
	result, err := s.session.RunTurnWithImages(ctx, text, images)
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

func (s *tuiSessionService) SetPermissionMode(mode string) error {
	return s.permissions.SetMode(mode)
}

// SetModel switches the session's model for subsequent turns and updates the
// cached config so the status line reflects it. The switch is runtime-only and
// not persisted to config files.
func (s *tuiSessionService) SetModel(model string) error {
	if err := s.session.SetModel(model); err != nil {
		return err
	}
	s.cfg.Model = strings.TrimSpace(model)
	return nil
}

func (s *tuiSessionService) Close(ctx context.Context) error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.session != nil {
		s.session.FireSessionEnd(ctx, "exit")
	}
	return closeRecorders(ctx, s.resources.Telemetry, s.resources.Transcript, s.resources.Sessions, s.resources.Backend, s.resources.MCP, s.resources.Shells)
}

func (s *tuiSessionService) Status() ui.Status {
	return ui.Status{
		Model:              s.cfg.Model,
		CWD:                s.cfg.CWD,
		PermissionMode:     s.permissions.Mode(),
		Transcript:         s.cfg.Transcript,
		SessionID:          s.resources.SessionID,
		InputPricePerMTok:  s.cfg.InputPrice,
		OutputPricePerMTok: s.cfg.OutputPrice,
		PricingSource:      s.cfg.PriceSource,
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
