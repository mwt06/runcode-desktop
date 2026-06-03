package ui

import (
	"context"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/wt68/runcode/pkg/tool"
)

const (
	eventBufferSize      = 1024
	mouseWheelScrollRows = 3
	bottomChromeHeight   = 4
	maxToolStoredFiles   = 50
	maxToolStoredOutput  = 50
	toolLineMaxRunes     = 200
	toolFilePathMaxRunes = 96
)

type Model struct {
	service Service
	status  Status

	width  int
	height int

	viewport viewport.Model
	input    textinput.Model

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
		toolMessages:     map[string]int{},
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
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.approval != nil {
		return m.handleApprovalKey(msg)
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
	case tea.KeyCtrlO:
		m.toolDetailsExpanded = !m.toolDetailsExpanded
		m.refreshViewport()
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
		m.appendMessage(RoleSystem, statusText(m.status, m.state(), m.turnCount, len(m.messages), m.totalInputTokens, m.totalOutputTokens, m.lastReasoningScenario, m.lastReasoningConfidence))
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
	m.input.Width = inputWidth
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

func (m *Model) applyToolEvent(event tool.Event) {
	if event.Type == "" {
		return
	}
	key := toolProgressKey(event)
	idx, ok := m.toolMessages[key]
	if !ok || m.toolProgressAt(idx, key) == nil {
		progress := &ToolProgress{
			ToolName:  event.ToolName,
			ToolUseID: event.ToolUseID,
			Status:    ToolStatusRunning,
		}
		idx = m.toolBatchMessageIndex()
		m.messages[idx].Tools = append(m.messages[idx].Tools, progress)
		m.toolMessages[key] = idx
	}
	progress := m.toolProgressAt(idx, key)
	if progress == nil {
		return
	}
	if progress.ToolName == "" {
		progress.ToolName = event.ToolName
	}
	if progress.ToolUseID == "" {
		progress.ToolUseID = event.ToolUseID
	}
	if progress.StartedAt.IsZero() && !event.Time.IsZero() {
		progress.StartedAt = event.Time
	}
	appendToolFileReferences(progress, event.Files, event.FilesTotal)
	appendToolOutput(progress, event.Output, event.OutputTotal, event.OutputTruncated)
	message := toolEventMessage(event)
	switch event.Type {
	case tool.EventTypeStarted:
		progress.Status = ToolStatusRunning
		progress.Message = message
		if progress.StartedAt.IsZero() {
			progress.StartedAt = event.Time
		}
	case tool.EventTypeProgress, tool.EventTypeOutput:
		progress.Status = ToolStatusRunning
		progress.Message = message
	case tool.EventTypeCompleted:
		progress.Status = ToolStatusCompleted
		progress.Message = message
		progress.FinishedAt = event.Time
	case tool.EventTypeFailed:
		progress.Status = ToolStatusFailed
		progress.Message = message
		progress.FinishedAt = event.Time
	}
}

func (m *Model) toolBatchMessageIndex() int {
	m.closeCurrentAssistantForToolEvent()
	if len(m.messages) > 0 {
		idx := len(m.messages) - 1
		if m.messages[idx].Role == RoleTool {
			return idx
		}
	}
	m.messages = append(m.messages, ChatMessage{Role: RoleTool})
	return len(m.messages) - 1
}

func (m *Model) toolProgressAt(idx int, key string) *ToolProgress {
	if idx < 0 || idx >= len(m.messages) || m.messages[idx].Role != RoleTool {
		return nil
	}
	message := &m.messages[idx]
	if message.Tool != nil {
		return message.Tool
	}
	for _, progress := range message.Tools {
		if progress != nil && toolProgressKeyFromValues(progress.ToolName, progress.ToolUseID) == key {
			return progress
		}
	}
	return nil
}

func (m *Model) closeCurrentAssistantForToolEvent() {
	if m.currentAssistant < 0 || m.currentAssistant >= len(m.messages) {
		m.currentAssistant = -1
		return
	}
	current := m.messages[m.currentAssistant]
	if current.Role != RoleAssistant || !current.Streaming {
		m.currentAssistant = -1
		return
	}
	if strings.TrimSpace(current.Text) == "" {
		removed := m.currentAssistant
		m.messages = append(m.messages[:removed], m.messages[removed+1:]...)
		m.reindexToolMessagesAfterRemoval(removed)
	} else {
		m.messages[m.currentAssistant].Streaming = false
	}
	m.currentAssistant = -1
}

func (m *Model) reindexToolMessagesAfterRemoval(removed int) {
	for key, idx := range m.toolMessages {
		if idx == removed {
			delete(m.toolMessages, key)
			continue
		}
		if idx > removed {
			m.toolMessages[key] = idx - 1
		}
	}
}

func toolProgressKey(event tool.Event) string {
	return toolProgressKeyFromValues(event.ToolName, event.ToolUseID)
}

func toolProgressKeyFromValues(toolName string, toolUseID string) string {
	if toolUseID != "" {
		return "id:" + toolUseID
	}
	if toolName != "" {
		return "name:" + toolName
	}
	return "unknown"
}

func appendToolFileReferences(progress *ToolProgress, refs []tool.FileReference, total int) {
	if progress == nil {
		return
	}
	seen := make(map[string]struct{}, len(progress.Files)+len(refs))
	for _, file := range progress.Files {
		seen[file.Path] = struct{}{}
	}
	for _, ref := range refs {
		path, ok := safeToolFilePath(ref.Path)
		if !ok {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		progress.Files = append(progress.Files, ToolFileReference{Path: path, Kind: string(ref.Kind)})
		if len(progress.Files) >= maxToolStoredFiles {
			break
		}
	}
	if len(progress.Files) > 0 && total > progress.FilesTotal {
		progress.FilesTotal = total
	}
	if progress.FilesTotal < len(progress.Files) {
		progress.FilesTotal = len(progress.Files)
	}
}

func safeToolFilePath(value string) (string, bool) {
	value = strings.TrimSpace(filepath.ToSlash(value))
	if value == "" || filepath.IsAbs(value) || strings.HasPrefix(value, "/") || strings.Contains(value, ":") {
		return "", false
	}
	segments := strings.Split(value, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
		for _, r := range segment {
			if r == '\n' || r == '\r' || unicode.IsControl(r) {
				return "", false
			}
		}
	}
	return truncateRunes(strings.Join(segments, "/"), toolFilePathMaxRunes), true
}

func toolEventMessage(event tool.Event) string {
	message := strings.TrimSpace(event.Message)
	if message != "" {
		return message
	}
	switch event.Type {
	case tool.EventTypeStarted:
		return "started"
	case tool.EventTypeProgress:
		return "running"
	case tool.EventTypeOutput:
		return "output"
	case tool.EventTypeCompleted:
		return "completed"
	case tool.EventTypeFailed:
		return "failed"
	default:
		return string(event.Type)
	}
}

func appendToolOutput(progress *ToolProgress, lines []tool.OutputLine, total int, truncated bool) {
	if progress == nil || len(lines) == 0 {
		return
	}
	for _, line := range lines {
		if len(progress.Output) >= maxToolStoredOutput {
			truncated = true
			break
		}
		progress.Output = append(progress.Output, ToolOutputLine{Stream: string(line.Stream), Text: safeOutputText(line.Text)})
	}
	if total > progress.OutputTotal {
		progress.OutputTotal = total
	}
	if progress.OutputTotal < len(progress.Output) {
		progress.OutputTotal = len(progress.Output)
	}
	if truncated {
		progress.OutputTruncated = true
	}
}

func safeOutputText(text string) string {
	text = strings.ReplaceAll(text, "\t", "    ")
	var b strings.Builder
	for _, r := range text {
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return truncateRunes(b.String(), toolLineMaxRunes)
}

func truncateRunes(value string, width int) string {
	if width <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
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
