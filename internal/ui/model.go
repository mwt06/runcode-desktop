package ui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	eventBufferSize      = 1024
	mouseWheelScrollRows = 3
)

type Model struct {
	service Service
	status  Status

	width  int
	height int

	viewport viewport.Model
	input    textinput.Model

	messages           []ChatMessage
	inFlight           bool
	currentAssistant   int
	events             chan tea.Msg
	turnCancel         context.CancelFunc
	exitingAfterCancel bool
	followOutput       bool

	lastError string
	turnCount int
}

func New(service Service) Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "输入你的问题，/help 查看命令"
	input.Focus()
	input.CharLimit = 0
	input.Width = 80
	vp := viewport.New(80, 20)
	vp.MouseWheelEnabled = true
	vp.MouseWheelDelta = mouseWheelScrollRows
	return Model{
		service:          service,
		status:           service.Status(),
		viewport:         vp,
		input:            input,
		currentAssistant: -1,
		events:           make(chan tea.Msg, eventBufferSize),
		followOutput:     true,
	}
}

func (m Model) Events() chan<- tea.Msg {
	return m.events
}

func AssistantDelta(text string) tea.Msg {
	return streamDeltaMsg{Text: text}
}

func (m Model) Init() tea.Cmd {
	return waitEventCmd(m.events)
}

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
		m.lastError = ""
		m.refreshViewport()
		return m, nil
	case resetErrorMsg:
		m.appendMessage(RoleError, errorText(msg.Err))
		m.refreshViewport()
		return m, nil
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		return m.submitInput()
	case tea.KeyPgUp:
		m.scrollUp(m.viewport.Height / 2)
		return m, nil
	case tea.KeyPgDown:
		m.scrollDown(m.viewport.Height / 2)
		return m, nil
	case tea.KeyUp:
		m.scrollUp(1)
		return m, nil
	case tea.KeyDown:
		m.scrollDown(1)
		return m, nil
	case tea.KeyHome:
		m.viewport.GotoTop()
		m.followOutput = false
		return m, nil
	case tea.KeyEnd:
		m.viewport.GotoBottom()
		m.followOutput = true
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
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
	m.input.SetValue("")

	switch parseSlashCommand(text) {
	case slashHelp:
		m.appendMessage(RoleSystem, helpText())
		m.refreshViewport()
		return m, nil
	case slashStatus:
		m.appendMessage(RoleSystem, statusText(m.status, m.state(), m.turnCount, len(m.messages)))
		m.refreshViewport()
		return m, nil
	case slashClear:
		if m.inFlight {
			m.appendMessage(RoleSystem, "cannot clear while assistant is responding")
			m.refreshViewport()
			return m, nil
		}
		return m, resetCmd(m.service)
	case slashExit:
		if m.inFlight && m.turnCancel != nil {
			m.exitingAfterCancel = true
			m.turnCancel()
			return m, nil
		}
		return m, tea.Quit
	case slashUnknown:
		m.appendMessage(RoleError, "unknown command: "+text)
		m.refreshViewport()
		return m, nil
	}

	if m.inFlight {
		m.appendMessage(RoleSystem, "assistant is still responding")
		m.refreshViewport()
		return m, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.turnCancel = cancel
	m.inFlight = true
	m.lastError = ""
	m.appendMessage(RoleUser, text)
	m.messages = append(m.messages, ChatMessage{Role: RoleAssistant, Streaming: true})
	m.currentAssistant = len(m.messages) - 1
	m.followOutput = true
	m.refreshViewport()
	return m, runTurnCmd(ctx, m.service, text, m.events)
}

func (m *Model) resize() {
	viewportHeight := m.height - 2
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	m.viewport.Width = m.width
	m.viewport.Height = viewportHeight
	inputWidth := m.width - 2
	if inputWidth < 1 {
		inputWidth = 1
	}
	m.input.Width = inputWidth
}

func (m *Model) appendMessage(role Role, text string) {
	m.messages = append(m.messages, ChatMessage{Role: role, Text: text})
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
	}
	m.inFlight = false
	m.turnCancel = nil
	m.currentAssistant = -1
	m.turnCount++
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
	m.viewport.SetContent(renderMessages(m.messages))
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

func waitEventCmd(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-events
	}
}

func runTurnCmd(ctx context.Context, service Service, text string, events chan<- tea.Msg) tea.Cmd {
	return func() tea.Msg {
		go func() {
			result, err := service.RunTurn(ctx, text)
			if err != nil {
				events <- turnErrorMsg{Err: err}
				return
			}
			events <- turnDoneMsg{Result: result}
		}()
		return nil
	}
}

func resetCmd(service Service) tea.Cmd {
	return func() tea.Msg {
		if err := service.Reset(context.Background()); err != nil {
			return resetErrorMsg{Err: err}
		}
		return resetDoneMsg{}
	}
}
