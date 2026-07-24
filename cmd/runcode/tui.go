package main

import (
	"context"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/wt68/runcode/internal/ui"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
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
	defer func() { _ = backend.Close(context.Background()) }()
	infos, err := backend.List(context.Background())
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
	defer func() { _ = service.Close(ctx) }()

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
	cfg        chatConfig
	session    *engine.Session
	onDelta    func(string)
	approver   *ui.Approver
	toolEvents chan tool.Event
	closed     bool
}

func newTuiSessionService(cfg chatConfig) (*tuiSessionService, error) {
	service := &tuiSessionService{cfg: cfg, toolEvents: make(chan tool.Event, tuiToolEventBufferSize)}
	// Always build the approver and a mode-aware interactive authorizer so /mode
	// can switch to interactive at runtime, even when starting in safe mode.
	service.approver = ui.NewApprover(cfg.CWD)
	store, err := engine.NewAllowStore(cfg.CWD)
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
	session, err := engine.Build(cfg, engine.Options{
		Permissions: permissionService,
		StreamDelta: func(delta string) {
			if service.onDelta != nil {
				service.onDelta(delta)
			}
		},
		ToolEvents: service.toolEvents,
		// The TUI renders warnings inside the conversation, so startup warnings are
		// discarded here; telemetry still goes to the real stderr.
		Warn:            io.Discard,
		TelemetryWriter: os.Stderr,
	})
	if err != nil {
		return nil, err
	}
	service.session = session
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
	before, after, _, err := s.session.Compact(ctx)
	if err != nil {
		return ui.CompactResult{}, err
	}
	return ui.CompactResult{Before: before, After: after}, nil
}

func (s *tuiSessionService) SetPermissionMode(mode string) error {
	return s.session.SetPermissionMode(mode)
}

// SetModel switches the session's model for subsequent turns. The switch is
// runtime-only and not persisted to config files; the status line reflects it via
// the engine's live model.
func (s *tuiSessionService) SetModel(model string) error {
	return s.session.SetModel(model)
}

func (s *tuiSessionService) Close(ctx context.Context) error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.session.Close(ctx)
}

func (s *tuiSessionService) Status() ui.Status {
	st := s.session.Status()
	return ui.Status{
		Model:              st.Model,
		CWD:                st.CWD,
		PermissionMode:     st.PermissionMode,
		Transcript:         st.Transcript,
		SessionID:          st.SessionID,
		MaxContextTokens:   st.MaxContextTokens,
		InputPricePerMTok:  st.InputPricePerMTok,
		OutputPricePerMTok: st.OutputPricePerMTok,
		PricingSource:      st.PricingSource,
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
