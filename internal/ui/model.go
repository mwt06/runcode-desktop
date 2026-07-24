package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/wt68/runcode/internal/command"
)

const (
	eventBufferSize      = 1024
	mouseWheelScrollRows = 3
	bottomChromeHeight   = 4
	// maxInputRows caps how tall the multi-line input grows before it scrolls
	// internally, keeping the conversation viewport usable on small terminals.
	maxInputRows = 8
)

// Model is the bubbletea model behind the TUI: it owns the transcript, the input
// box, the approval queue and the per-turn counters shown in the status line.
// Everything it needs from the engine goes through Service.
type Model struct {
	service  Service
	status   Status
	commands *slashRegistry

	width  int
	height int

	viewport viewport.Model
	input    textarea.Model
	history  promptHistory

	messages            []ChatMessage
	inFlight            bool
	currentAssistant    int
	toolMessages        map[string]int
	toolDetailsExpanded bool
	events              chan tea.Msg
	turnCancel          context.CancelFunc
	exitingAfterCancel  bool
	followOutput        bool

	approval      *pendingApproval
	approvalQueue []*pendingApproval

	lastError               string
	turnCount               int
	totalInputTokens        int
	totalOutputTokens       int
	lastReasoningScenario   string
	lastReasoningConfidence string

	// Slash command menu shown while typing a command name.
	menuActive   bool
	menuItems    []*slashCommand
	menuSelected int
}

// Option customizes a Model at construction.
type Option func(*Model)

// CustomCommand is a user-defined slash command: its name, a one-line summary for
// help, and a prompt-template body expanded and submitted on invocation.
type CustomCommand struct {
	Name    string
	Summary string
	Body    string
}

// WithCustomCommands registers user-defined slash commands. A custom command
// whose name collides with a built-in is skipped so built-ins always win.
func WithCustomCommands(cmds []CustomCommand) Option {
	return func(m *Model) {
		for _, c := range cmds {
			if _, exists := m.commands.lookup(c.Name); exists {
				continue
			}
			body := c.Body
			m.commands.register(&slashCommand{
				name:    c.Name,
				summary: c.Summary,
				run: func(mm Model, args []string) (Model, tea.Cmd) {
					model, cmd := mm.startTurn(command.Expand(body, args))
					return model.(Model), cmd
				},
			})
		}
	}
}

// New builds the TUI model around a session service, applying any options.
func New(service Service, opts ...Option) Model {
	input := textarea.New()
	input.Prompt = "> "
	input.Placeholder = "输入你的问题，/help 查看命令（alt+enter 或 ctrl+j 换行）"
	input.ShowLineNumbers = false
	input.CharLimit = 0
	input.MaxHeight = maxInputRows
	// Enter submits the turn; newlines are inserted with alt+enter / ctrl+j so a
	// single Enter never accidentally sends a half-written multi-line prompt.
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	// The input is a plain line box, not a document editor: drop the cursor-line
	// highlight and end-of-buffer filler so a one-line prompt looks unchanged.
	input.FocusedStyle.CursorLine = lipgloss.NewStyle()
	input.BlurredStyle.CursorLine = lipgloss.NewStyle()
	input.FocusedStyle.Prompt = mutedStyle
	input.BlurredStyle.Prompt = mutedStyle
	input.SetHeight(1)
	input.Focus()
	vp := viewport.New(80, 20)
	vp.MouseWheelEnabled = true
	vp.MouseWheelDelta = mouseWheelScrollRows
	m := Model{
		service:          service,
		status:           service.Status(),
		commands:         defaultSlashRegistry(),
		viewport:         vp,
		input:            input,
		currentAssistant: -1,
		toolMessages:     map[string]int{},
		events:           make(chan tea.Msg, eventBufferSize),
		followOutput:     true,
	}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

// Events is the channel the caller pushes engine events into (stream deltas,
// tool events, approval requests); the model pumps it via waitEventCmd.
func (m Model) Events() chan<- tea.Msg {
	return m.events
}

// AssistantDelta wraps a streamed text chunk as a model message.
func AssistantDelta(text string) tea.Msg {
	return streamDeltaMsg{Text: text}
}

// Init starts the event pump.
func (m Model) Init() tea.Cmd {
	return waitEventCmd(m.events)
}

// Update handles one bubbletea message and returns the next model state.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		m.refreshViewport()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case streamDeltaMsg:
		m.appendAssistantDelta(msg.Text)
		m.refreshViewport()
		return m, waitEventCmd(m.events)
	case toolEventMsg:
		m.applyToolEvent(msg.Event)
		m.refreshViewport()
		return m, waitEventCmd(m.events)
	case approvalRequestMsg:
		m.enqueueApproval(msg)
		m.relayout()
		return m, waitEventCmd(m.events)
	case turnDoneMsg:
		m.finishTurn(msg.Result)
		m.refreshViewport()
		if m.exitingAfterCancel {
			return m, tea.Quit
		}
		return m, waitEventCmd(m.events)
	case turnErrorMsg:
		m.finishTurnError(msg.Err)
		m.refreshViewport()
		if m.exitingAfterCancel {
			return m, tea.Quit
		}
		return m, waitEventCmd(m.events)
	case resetDoneMsg:
		m.messages = []ChatMessage{{Role: RoleSystem, Text: "history cleared"}}
		m.toolMessages = map[string]int{}
		m.lastError = ""
		m.totalInputTokens = 0
		m.totalOutputTokens = 0
		m.lastReasoningScenario = ""
		m.lastReasoningConfidence = ""
		m.refreshViewport()
		return m, nil
	case resetErrorMsg:
		m.appendMessage(RoleError, errorText(msg.Err))
		m.refreshViewport()
		return m, nil
	case compactDoneMsg:
		if msg.Result.After < msg.Result.Before {
			m.appendMessage(RoleSystem, fmt.Sprintf("context compacted: %d → %d messages", msg.Result.Before, msg.Result.After))
		} else {
			m.appendMessage(RoleSystem, "nothing to compact yet")
		}
		m.refreshViewport()
		return m, nil
	case compactErrorMsg:
		m.appendMessage(RoleError, errorText(msg.Err))
		m.refreshViewport()
		return m, nil
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.approval != nil {
		return m.handleApprovalKey(msg)
	}
	if m.menuActive {
		if handled, model, cmd := m.handleMenuKey(msg); handled {
			return model, cmd
		}
	}
	switch msg.Type {
	case tea.KeyCtrlC:
		if m.inFlight && m.turnCancel != nil {
			m.exitingAfterCancel = true
			m.turnCancel()
			return m, nil
		}
		return m, tea.Quit
	case tea.KeyEsc:
		if m.inFlight && m.turnCancel != nil {
			m.turnCancel()
			return m, nil
		}
	case tea.KeyEnter:
		// alt+enter inserts a newline; it falls through to the textarea below.
		if msg.Alt {
			break
		}
		return m.submitInput()
	case tea.KeyPgUp:
		m.scrollUp(m.viewport.Height / 2)
		return m, nil
	case tea.KeyPgDown:
		m.scrollDown(m.viewport.Height / 2)
		return m, nil
	case tea.KeyUp:
		return m.handleInputUp(msg)
	case tea.KeyDown:
		return m.handleInputDown(msg)
	case tea.KeyHome:
		m.viewport.GotoTop()
		m.followOutput = false
		return m, nil
	case tea.KeyEnd:
		m.viewport.GotoBottom()
		m.followOutput = true
		return m, nil
	case tea.KeyCtrlO:
		m.toolDetailsExpanded = !m.toolDetailsExpanded
		m.refreshViewport()
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m = m.afterInputEdit()
	return m, cmd
}

// handleInputUp routes the Up key: within a multi-line input it moves the cursor
// up; on the first line it recalls the previous (older) history entry. Viewport
// scrolling lives on PgUp/Home and the mouse wheel.
func (m Model) handleInputUp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.input.Line() > 0 {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m.afterInputEdit(), cmd
	}
	if text, ok := m.history.older(m.input.Value()); ok {
		m.recallHistory(text)
	}
	return m, nil
}

// handleInputDown is the mirror of handleInputUp: cursor down within the input,
// or the next (newer) history entry / saved draft when on the last line.
func (m Model) handleInputDown(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.input.Line() < m.input.LineCount()-1 {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m.afterInputEdit(), cmd
	}
	if text, ok := m.history.newer(); ok {
		m.recallHistory(text)
	}
	return m, nil
}

// recallHistory replaces the input with a recalled line, parks the cursor at the
// end, and re-syncs the menu and input height.
func (m *Model) recallHistory(text string) {
	m.input.SetValue(text)
	m.input.CursorEnd()
	updated := m.afterInputEdit()
	*m = updated
}

// afterInputEdit re-syncs the slash-command menu and the input height after the
// input value may have changed, relaying out only when the bottom chrome height
// actually changed (so a normal keystroke does not re-render the whole viewport).
func (m Model) afterInputEdit() Model {
	prevActive, prevLen := m.menuActive, len(m.menuItems)
	m = m.syncCommandMenu()
	heightChanged := m.adjustInputHeight()
	if heightChanged || m.menuActive != prevActive || len(m.menuItems) != prevLen {
		m.relayout()
	}
	return m
}

// adjustInputHeight grows or shrinks the input box to fit its content, clamped to
// [1, maxInputRows]. It reports whether the height changed so callers can decide
// to relayout.
func (m *Model) adjustInputHeight() bool {
	rows := m.input.LineCount()
	if rows < 1 {
		rows = 1
	}
	if rows > maxInputRows {
		rows = maxInputRows
	}
	if rows == m.input.Height() {
		return false
	}
	m.input.SetHeight(rows)
	return true
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	before := m.viewport.YOffset
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	if m.viewport.YOffset != before {
		m.followOutput = m.viewport.AtBottom()
	}
	return m, cmd
}

func (m *Model) scrollUp(lines int) {
	m.viewport.ScrollUp(lines)
	m.followOutput = false
}

func (m *Model) scrollDown(lines int) {
	m.viewport.ScrollDown(lines)
	m.followOutput = m.viewport.AtBottom()
}

func (m Model) submitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	m.history.add(text)
	m.input.SetValue("")
	m.adjustInputHeight()
	m.menuActive = false

	if name, args, isSlash := parseSlash(text); isSlash {
		if command, ok := m.commands.lookup(name); ok {
			return command.run(m, args)
		}
		m.appendMessage(RoleError, "unknown command: "+text)
		m.refreshViewport()
		return m, nil
	}

	return m.startTurn(text)
}

// startTurn submits text as a user turn: it records the message, marks a turn in
// flight, and kicks off the streaming command. It is shared by free-text input
// and custom slash commands (which expand to a prompt).
func (m Model) startTurn(text string) (tea.Model, tea.Cmd) {
	if m.inFlight {
		m.appendMessage(RoleSystem, "assistant is still responding")
		m.refreshViewport()
		return m, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.turnCancel = cancel
	m.inFlight = true
	m.lastError = ""
	m.trimHistory()
	m.appendMessage(RoleUser, text)
	m.messages = append(m.messages, ChatMessage{Role: RoleAssistant, Streaming: true})
	m.currentAssistant = len(m.messages) - 1
	m.followOutput = true
	m.refreshViewport()
	return m, runTurnCmd(ctx, m.service, text, m.events)
}

func (m *Model) resize() {
	viewportHeight := m.height - m.chromeHeight()
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	m.viewport.Width = m.width
	m.viewport.Height = viewportHeight
	inputWidth := m.width - 2
	if inputWidth < 1 {
		inputWidth = 1
	}
	m.input.SetWidth(inputWidth)
}

func (m Model) chromeHeight() int {
	return len(m.bottomBlock())
}

func (m *Model) relayout() {
	m.resize()
	m.refreshViewport()
}

func (m *Model) appendMessage(role Role, text string) {
	m.messages = append(m.messages, ChatMessage{Role: role, Text: text})
}

// maxRetainedMessages bounds how many chat entries the UI keeps in memory. A very
// long session would otherwise grow m.messages without limit.
const maxRetainedMessages = 1000

// trimHistory drops the oldest entries once the buffer exceeds the cap, leaving a
// marker so the user knows earlier history scrolled off. It must be called only
// between turns: no streaming index (currentAssistant, tool message indices) may
// be live, since trimming shifts every absolute index. currentAssistant is reset
// to -1 because nothing is streaming between turns.
func (m *Model) trimHistory() {
	if len(m.messages) <= maxRetainedMessages {
		return
	}
	remove := len(m.messages) - maxRetainedMessages
	trimmed := make([]ChatMessage, 0, maxRetainedMessages+1)
	trimmed = append(trimmed, ChatMessage{Role: RoleSystem, Text: "earlier messages trimmed"})
	trimmed = append(trimmed, m.messages[remove:]...)
	m.messages = trimmed
	m.currentAssistant = -1
}

func (m *Model) appendAssistantDelta(text string) {
	if text == "" {
		return
	}
	if m.currentAssistant < 0 || m.currentAssistant >= len(m.messages) || m.messages[m.currentAssistant].Role != RoleAssistant {
		m.messages = append(m.messages, ChatMessage{Role: RoleAssistant, Streaming: true})
		m.currentAssistant = len(m.messages) - 1
	}
	m.messages[m.currentAssistant].Text += text
}

func (m *Model) finishTurn(result TurnResult) {
	if m.currentAssistant >= 0 && m.currentAssistant < len(m.messages) {
		if result.Text != "" && m.messages[m.currentAssistant].Text != result.Text {
			m.messages[m.currentAssistant].Text = result.Text
		}
		m.messages[m.currentAssistant].Streaming = false
	} else if strings.TrimSpace(result.Text) != "" {
		m.messages = append(m.messages, ChatMessage{Role: RoleAssistant, Text: result.Text})
	}
	m.inFlight = false
	m.turnCancel = nil
	m.currentAssistant = -1
	m.turnCount++
	m.totalInputTokens += result.InputTokens
	m.totalOutputTokens += result.OutputTokens
	m.lastReasoningScenario = result.ReasoningScenario
	m.lastReasoningConfidence = result.ReasoningConfidence
}

func (m *Model) finishTurnError(err error) {
	if m.currentAssistant >= 0 && m.currentAssistant < len(m.messages) {
		m.messages[m.currentAssistant].Streaming = false
		if strings.TrimSpace(m.messages[m.currentAssistant].Text) == "" {
			m.messages = append(m.messages[:m.currentAssistant], m.messages[m.currentAssistant+1:]...)
		}
	}
	m.inFlight = false
	m.turnCancel = nil
	m.currentAssistant = -1
	m.lastError = errorText(err)
	m.appendMessage(RoleError, m.lastError)
}

func (m *Model) refreshViewport() {
	m.viewport.SetContent(renderMessages(m.messages, m.viewport.Width, m.toolDetailsExpanded))
	if m.followOutput {
		m.viewport.GotoBottom()
	}
}

func (m Model) state() string {
	if m.exitingAfterCancel {
		return "cancelling"
	}
	if m.inFlight {
		return "responding"
	}
	return "idle"
}
