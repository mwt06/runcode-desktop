package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

func TestTrimHistoryCapsMessages(t *testing.T) {
	t.Parallel()
	m := New(&fakeService{})
	m.messages = make([]ChatMessage, maxRetainedMessages+50)
	for i := range m.messages {
		m.messages[i] = ChatMessage{Role: RoleUser, Text: "x"}
	}
	m.currentAssistant = len(m.messages) - 1

	m.trimHistory()

	if len(m.messages) != maxRetainedMessages+1 {
		t.Fatalf("messages = %d, want %d (cap + marker)", len(m.messages), maxRetainedMessages+1)
	}
	if m.messages[0].Role != RoleSystem || !strings.Contains(m.messages[0].Text, "trimmed") {
		t.Fatalf("first message should be the trim marker, got %#v", m.messages[0])
	}
	if m.currentAssistant != -1 {
		t.Fatalf("currentAssistant = %d, want -1 after trim", m.currentAssistant)
	}
}

func TestCustomCommandExpandsAndStartsTurn(t *testing.T) {
	t.Parallel()
	m := New(&fakeService{}, WithCustomCommands([]CustomCommand{
		{Name: "greet", Summary: "say hi", Body: "Say hello to $ARGUMENTS"},
	}))

	// The command is registered and shows in help.
	if _, ok := m.commands.lookup("greet"); !ok {
		t.Fatal("custom command not registered")
	}
	cmd, ok := m.commands.lookup("greet")
	if !ok {
		t.Fatal("lookup greet failed")
	}
	mm, teaCmd := cmd.run(m, []string{"Bob"})
	if !mm.inFlight {
		t.Fatal("custom command should start a turn (inFlight)")
	}
	if teaCmd == nil {
		t.Fatal("expected a turn command")
	}
	// The expanded prompt is recorded as the user message.
	last := mm.messages[len(mm.messages)-2] // user msg precedes the streaming assistant
	if last.Role != RoleUser || last.Text != "Say hello to Bob" {
		t.Fatalf("user message = %#v, want expanded prompt", last)
	}
}

func TestCustomCommandDoesNotOverrideBuiltin(t *testing.T) {
	t.Parallel()
	m := New(&fakeService{}, WithCustomCommands([]CustomCommand{
		{Name: "help", Summary: "evil override", Body: "do something"},
	}))
	cmd, ok := m.commands.lookup("help")
	if !ok {
		t.Fatal("help missing")
	}
	if cmd.summary == "evil override" {
		t.Fatal("custom command must not override the built-in /help")
	}
}

func TestTrimHistoryNoopUnderCap(t *testing.T) {
	t.Parallel()
	m := New(&fakeService{})
	m.messages = []ChatMessage{{Role: RoleUser, Text: "a"}, {Role: RoleAssistant, Text: "b"}}
	m.currentAssistant = 1

	m.trimHistory()

	if len(m.messages) != 2 || m.currentAssistant != 1 {
		t.Fatalf("under-cap history should be untouched: len=%d currentAssistant=%d", len(m.messages), m.currentAssistant)
	}
}

type fakeService struct {
	status              Status
	resetCount          int
	closeCount          int
	compactResult       CompactResult
	compactErr          error
	permissionMode      string
	permissionModeCalls int
	permissionModeErr   error
	model               string
	modelCalls          int
	modelErr            error
}

func (s *fakeService) RunTurn(context.Context, string) (TurnResult, error) {
	return TurnResult{Text: "done"}, nil
}

func (s *fakeService) Reset(context.Context) error {
	s.resetCount++
	return nil
}

func (s *fakeService) Close(context.Context) error {
	s.closeCount++
	return nil
}

func (s *fakeService) Compact(context.Context) (CompactResult, error) {
	return s.compactResult, s.compactErr
}

func (s *fakeService) SetPermissionMode(mode string) error {
	s.permissionMode = mode
	s.permissionModeCalls++
	return s.permissionModeErr
}

func (s *fakeService) SetModel(model string) error {
	if s.modelErr != nil {
		return s.modelErr
	}
	s.model = model
	s.modelCalls++
	return nil
}

func (s *fakeService) Status() Status {
	return s.status
}

func TestHelpCommandAppendsHelpMessage(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	model.input.SetValue("/help")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	if len(model.messages) != 1 || model.messages[0].Role != RoleSystem || !strings.Contains(model.messages[0].Text, "/clear") {
		t.Fatalf("messages = %#v, want help system message", model.messages)
	}
}

func TestStatusCommandAppendsStatusMessage(t *testing.T) {
	t.Parallel()

	service := &fakeService{status: Status{Model: "claude-test", CWD: "/repo", PermissionMode: "safe", Transcript: "off"}}
	model := New(service)
	model.input.SetValue("/status")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	if len(model.messages) != 1 || !strings.Contains(model.messages[0].Text, "claude-test") || !strings.Contains(model.messages[0].Text, "/repo") {
		t.Fatalf("status message = %#v", model.messages)
	}
}

func TestSubmitStartsTurn(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	model.input.SetValue("hello")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	if cmd == nil {
		t.Fatal("expected run turn command")
	}
	if !model.inFlight {
		t.Fatal("expected inFlight")
	}
	if len(model.messages) != 2 || model.messages[0].Role != RoleUser || model.messages[1].Role != RoleAssistant || !model.messages[1].Streaming {
		t.Fatalf("messages = %#v, want user and streaming assistant", model.messages)
	}
}

func TestStreamDeltaAppendsAssistantMessage(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	model.messages = []ChatMessage{{Role: RoleAssistant, Streaming: true}}
	model.currentAssistant = 0
	updated, _ := model.Update(streamDeltaMsg{Text: "hello"})
	model = updated.(Model)

	if model.messages[0].Text != "hello" {
		t.Fatalf("assistant text = %q, want hello", model.messages[0].Text)
	}
}

func TestTurnDoneMarksIdleAndReconcilesFinalText(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	model.inFlight = true
	model.currentAssistant = 0
	model.messages = []ChatMessage{{Role: RoleAssistant, Text: "hel", Streaming: true}}
	updated, _ := model.Update(turnDoneMsg{Result: TurnResult{Text: "hello"}})
	model = updated.(Model)

	if model.inFlight {
		t.Fatal("expected idle after turn done")
	}
	if model.messages[0].Streaming || model.messages[0].Text != "hello" {
		t.Fatalf("assistant message = %#v, want final hello and not streaming", model.messages[0])
	}
}

func TestTurnErrorMarksIdleAndAppendsError(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	model.inFlight = true
	model.currentAssistant = 0
	model.messages = []ChatMessage{{Role: RoleAssistant, Streaming: true}}
	updated, _ := model.Update(turnErrorMsg{Err: errors.New("boom")})
	model = updated.(Model)

	if model.inFlight {
		t.Fatal("expected idle after turn error")
	}
	if len(model.messages) != 1 || model.messages[0].Role != RoleError || !strings.Contains(model.messages[0].Text, "boom") {
		t.Fatalf("messages = %#v, want error message", model.messages)
	}
}

func TestClearCommandResetsServiceAndMessages(t *testing.T) {
	t.Parallel()

	service := &fakeService{}
	model := New(service)
	model.messages = []ChatMessage{{Role: RoleUser, Text: "old"}}
	model.input.SetValue("/clear")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected reset command")
	}
	msg := cmd()
	updated, _ = model.Update(msg)
	model = updated.(Model)

	if service.resetCount != 1 {
		t.Fatalf("resetCount = %d, want 1", service.resetCount)
	}
	if len(model.messages) != 1 || model.messages[0].Role != RoleSystem || !strings.Contains(model.messages[0].Text, "history cleared") {
		t.Fatalf("messages = %#v, want history cleared", model.messages)
	}
}

func TestScrollingUpDisablesFollowOutput(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	model.viewport.Height = 2
	model.messages = []ChatMessage{{Role: RoleAssistant, Text: strings.Repeat("line\n", 20)}}
	model.refreshViewport()
	bottom := model.viewport.YOffset

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(Model)

	if model.followOutput {
		t.Fatal("expected followOutput to be disabled after scrolling up")
	}
	if model.viewport.YOffset >= bottom {
		t.Fatalf("YOffset = %d, want less than bottom %d", model.viewport.YOffset, bottom)
	}
}

func TestEndKeyRestoresFollowOutput(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	model.viewport.Height = 2
	model.messages = []ChatMessage{{Role: RoleAssistant, Text: strings.Repeat("line\n", 20)}}
	model.refreshViewport()
	model.scrollUp(1)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnd})
	model = updated.(Model)

	if !model.followOutput {
		t.Fatal("expected followOutput after End")
	}
	if !model.viewport.AtBottom() {
		t.Fatalf("expected viewport at bottom, YOffset=%d", model.viewport.YOffset)
	}
}

func TestRefreshViewportKeepsManualScrollPosition(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	model.viewport.Height = 2
	model.messages = []ChatMessage{{Role: RoleAssistant, Text: strings.Repeat("line\n", 20)}}
	model.refreshViewport()
	model.scrollUp(3)
	offset := model.viewport.YOffset
	model.messages = append(model.messages, ChatMessage{Role: RoleSystem, Text: "new message"})
	model.refreshViewport()

	if model.viewport.YOffset != offset {
		t.Fatalf("YOffset = %d, want preserved %d", model.viewport.YOffset, offset)
	}
}

func TestToolEventCreatesAndUpdatesProgressMessage(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	model.messages = []ChatMessage{{Role: RoleUser, Text: "read"}, {Role: RoleAssistant, Streaming: true}}
	model.currentAssistant = 1
	updated, _ := model.Update(toolEventMsg{Event: tool.Event{Type: tool.EventTypeStarted, ToolName: "Read", ToolUseID: "toolu_123"}})
	model = updated.(Model)

	if len(model.messages) != 2 {
		t.Fatalf("messages = %#v, want user and tool", model.messages)
	}
	if model.messages[1].Role != RoleTool || len(model.messages[1].Tools) != 1 || model.messages[1].Tools[0].Status != ToolStatusRunning {
		t.Fatalf("tool message = %#v, want running tool batch", model.messages[1])
	}
	if model.currentAssistant != -1 {
		t.Fatalf("currentAssistant = %d, want -1", model.currentAssistant)
	}

	updated, _ = model.Update(toolEventMsg{Event: tool.Event{Type: tool.EventTypeProgress, ToolName: "Read", ToolUseID: "toolu_123", Message: "scanning"}})
	model = updated.(Model)
	updated, _ = model.Update(toolEventMsg{Event: tool.Event{Type: tool.EventTypeCompleted, ToolName: "Read", ToolUseID: "toolu_123"}})
	model = updated.(Model)

	if len(model.messages) != 2 {
		t.Fatalf("messages = %#v, want no duplicate tool card", model.messages)
	}
	progress := model.messages[1].Tools[0]
	if progress.Status != ToolStatusCompleted || progress.Message != "completed" {
		t.Fatalf("progress = %#v, want completed status", progress)
	}
}

func TestToolEventPreservesAssistantTextAndCreatesNewFinalAssistant(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	model.messages = []ChatMessage{{Role: RoleAssistant, Text: "I will inspect files", Streaming: true}}
	model.currentAssistant = 0
	updated, _ := model.Update(toolEventMsg{Event: tool.Event{Type: tool.EventTypeStarted, ToolName: "Glob", ToolUseID: "toolu_glob"}})
	model = updated.(Model)

	if model.messages[0].Role != RoleAssistant || model.messages[0].Streaming {
		t.Fatalf("assistant preface = %#v, want preserved non-streaming", model.messages[0])
	}
	if model.messages[1].Role != RoleTool {
		t.Fatalf("messages = %#v, want tool after assistant preface", model.messages)
	}
	updated, _ = model.Update(streamDeltaMsg{Text: "done"})
	model = updated.(Model)

	if len(model.messages) != 3 || model.messages[2].Role != RoleAssistant || model.messages[2].Text != "done" {
		t.Fatalf("messages = %#v, want final assistant after tool", model.messages)
	}
}

func TestToolEventUsesSeparateCardsForParallelToolUses(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	for _, id := range []string{"toolu_a", "toolu_b"} {
		updated, _ := model.Update(toolEventMsg{Event: tool.Event{Type: tool.EventTypeStarted, ToolName: "Grep", ToolUseID: id}})
		model = updated.(Model)
	}

	if len(model.messages) != 1 || len(model.messages[0].Tools) != 2 || model.messages[0].Tools[0].ToolUseID != "toolu_a" || model.messages[0].Tools[1].ToolUseID != "toolu_b" {
		t.Fatalf("messages = %#v, want one batched tool message", model.messages)
	}
}

func TestAssistantMarkdownIsRendered(t *testing.T) {
	t.Parallel()

	view := renderMessages([]ChatMessage{{Role: RoleAssistant, Text: "# Title\n\n- item\n\n```go\nfmt.Println(\"hello\")\n```"}}, 80)
	if !strings.Contains(view, "Assistant") || !strings.Contains(view, "Title") || !strings.Contains(view, "item") || !strings.Contains(view, "fmt") || !strings.Contains(view, "Println") {
		t.Fatalf("view = %q, want rendered markdown content", view)
	}
	if strings.Contains(view, "```go") {
		t.Fatalf("view = %q, should not contain raw fenced code marker", view)
	}
}

func TestAssistantMarkdownHeadingsDoNotRenderRawHashPrefixes(t *testing.T) {
	t.Parallel()

	view := renderMessages([]ChatMessage{{Role: RoleAssistant, Text: "## Section\n\n### Detail"}}, 80)
	if !strings.Contains(view, "Section") || !strings.Contains(view, "Detail") {
		t.Fatalf("view = %q, want heading text", view)
	}
	if strings.Contains(view, "## Section") || strings.Contains(view, "### Detail") {
		t.Fatalf("view = %q, should not contain raw heading markers", view)
	}
}

func TestNonAssistantMessagesDoNotRenderMarkdown(t *testing.T) {
	t.Parallel()

	messages := []ChatMessage{
		{Role: RoleUser, Text: "# raw user"},
		{Role: RoleSystem, Text: "# raw system"},
		{Role: RoleError, Text: "# raw error"},
	}
	view := renderMessages(messages, 80)
	for _, want := range []string{"# raw user", "# raw system", "# raw error"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view = %q, want %q preserved", view, want)
		}
	}
}

func TestStreamingAssistantPlaceholderDoesNotRequireMarkdown(t *testing.T) {
	t.Parallel()

	for _, width := range []int{0, 1, 80} {
		view := renderMessages([]ChatMessage{{Role: RoleAssistant, Streaming: true}}, width)
		if !strings.Contains(view, "Assistant") || !strings.Contains(view, "…") {
			t.Fatalf("width %d view = %q, want streaming placeholder", width, view)
		}
	}
}

func TestToolRenderShowsSafeFileSummary(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	updated, _ := model.Update(toolEventMsg{Event: tool.Event{
		Type:       tool.EventTypeCompleted,
		ToolName:   "Read",
		ToolUseID:  "toolu_123",
		Files:      []tool.FileReference{{Path: "internal/ui/render.go", Kind: tool.FileReferenceRead}},
		FilesTotal: 1,
	}})
	model = updated.(Model)

	view := renderMessages(model.messages, 80)
	if !strings.Contains(view, "Read 1 file · done") || !strings.Contains(view, "└ internal/ui/render.go") {
		t.Fatalf("view = %q, want file summary", view)
	}
}

func TestToolRenderLimitsAndExpandsFileSummary(t *testing.T) {
	t.Parallel()

	refs := []tool.FileReference{
		{Path: "a.go", Kind: tool.FileReferenceMatched},
		{Path: "b.go", Kind: tool.FileReferenceMatched},
		{Path: "c.go", Kind: tool.FileReferenceMatched},
		{Path: "d.go", Kind: tool.FileReferenceMatched},
	}
	model := New(&fakeService{})
	updated, _ := model.Update(toolEventMsg{Event: tool.Event{Type: tool.EventTypeCompleted, ToolName: "Grep", ToolUseID: "toolu_grep", Files: refs, FilesTotal: len(refs)}})
	model = updated.(Model)

	collapsed := renderMessages(model.messages, 80, false)
	if !strings.Contains(collapsed, "Grep 4 files · done") || !strings.Contains(collapsed, "(ctrl+o to expand)") || !strings.Contains(collapsed, "└ +1 more") || strings.Contains(collapsed, "d.go") {
		t.Fatalf("collapsed view = %q, want limited file summary", collapsed)
	}
	expanded := renderMessages(model.messages, 80, true)
	if !strings.Contains(expanded, "(ctrl+o to collapse)") || !strings.Contains(expanded, "d.go") || strings.Contains(expanded, "+1 more") {
		t.Fatalf("expanded view = %q, want expanded file summary", expanded)
	}
}

func TestToolRenderRejectsUnsafeFileReferences(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	updated, _ := model.Update(toolEventMsg{Event: tool.Event{
		Type:     tool.EventTypeCompleted,
		ToolName: "Read",
		Files: []tool.FileReference{
			{Path: "/abs/secret.txt", Kind: tool.FileReferenceRead},
			{Path: "../secret.txt", Kind: tool.FileReferenceRead},
			{Path: "bad\nname.go", Kind: tool.FileReferenceRead},
			{Path: "safe/file.go", Kind: tool.FileReferenceRead},
		},
		FilesTotal: 4,
	}})
	model = updated.(Model)

	view := renderMessages(model.messages, 80, true)
	for _, forbidden := range []string{"/abs/secret.txt", "../secret.txt", "bad\nname.go"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("view = %q, leaked unsafe path %q", view, forbidden)
		}
	}
	if !strings.Contains(view, "safe/file.go") {
		t.Fatalf("view = %q, want safe path", view)
	}
}

func TestToolRenderDoesNotExposeEventData(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	updated, _ := model.Update(toolEventMsg{Event: tool.Event{Type: tool.EventTypeProgress, ToolName: "Read", ToolUseID: "toolu_123", Message: "safe", Data: "secret-data"}})
	model = updated.(Model)

	view := renderMessages(model.messages, 80)
	if !strings.Contains(view, "Tools") || !strings.Contains(view, "Read ×1") || !strings.Contains(view, "safe") {
		t.Fatalf("view = %q, want grouped tool progress", view)
	}
	if strings.Contains(view, "secret-data") {
		t.Fatalf("view leaked event data: %q", view)
	}
}

func TestToolRenderBatchesConsecutiveToolMessages(t *testing.T) {
	t.Parallel()

	messages := []ChatMessage{
		{Role: RoleTool, Tool: &ToolProgress{ToolName: "Glob", Status: ToolStatusCompleted, Message: "completed"}},
		{Role: RoleTool, Tool: &ToolProgress{ToolName: "Glob", Status: ToolStatusCompleted, Message: "completed"}},
		{Role: RoleTool, Tool: &ToolProgress{ToolName: "Read", Status: ToolStatusCompleted, Message: "completed"}},
		{Role: RoleTool, Tool: &ToolProgress{ToolName: "Read", Status: ToolStatusCompleted, Message: "completed"}},
	}

	view := renderMessages(messages, 80)
	if strings.Count(view, "Tools") != 1 || !strings.Contains(view, "Glob ×2 · done") || !strings.Contains(view, "Read ×2 · done") {
		t.Fatalf("view = %q, want batched tool summary", view)
	}
	if strings.Contains(view, "completed") {
		t.Fatalf("view = %q, should hide repetitive completed messages", view)
	}
}

func TestClearWhileInFlightIsRejected(t *testing.T) {
	t.Parallel()

	service := &fakeService{}
	model := New(service)
	model.inFlight = true
	model.input.SetValue("/clear")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	if cmd != nil {
		t.Fatal("expected no reset command while in flight")
	}
	if service.resetCount != 0 {
		t.Fatalf("resetCount = %d, want 0", service.resetCount)
	}
	if len(model.messages) != 1 || !strings.Contains(model.messages[0].Text, "cannot clear") {
		t.Fatalf("messages = %#v, want cannot clear warning", model.messages)
	}
}

func TestViewRendersBottomStatusAndInputDividers(t *testing.T) {
	t.Parallel()

	service := &fakeService{status: Status{Model: "claude-test", CWD: "/Users/secret/project", PermissionMode: "safe", SessionID: "sess_1234567890"}}
	model := New(service)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(Model)

	view := model.View()
	lines := strings.Split(view, "\n")
	if len(lines) < bottomChromeHeight {
		t.Fatalf("view = %q, want bottom chrome", view)
	}
	if strings.Contains(lines[0], "Model:") || strings.Contains(lines[0], "runcode") {
		t.Fatalf("first line = %q, should not be old top status", lines[0])
	}
	for _, want := range []string{"Model: claude-test", "Ctx: -", "safe", "cwd project", "> "} {
		if !strings.Contains(view, want) {
			t.Fatalf("view = %q, want %q", view, want)
		}
	}
	if strings.Count(view, "─") < 120 {
		t.Fatalf("view = %q, want top and bottom input dividers", view)
	}
}

func TestResizeAccountsForBottomChromeRows(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(Model)
	if model.viewport.Height != 24-bottomChromeHeight {
		t.Fatalf("viewport height = %d, want %d", model.viewport.Height, 24-bottomChromeHeight)
	}
	for _, height := range []int{1, 2, 3} {
		updated, _ = model.Update(tea.WindowSizeMsg{Width: 20, Height: height})
		model = updated.(Model)
		if model.viewport.Height < 1 {
			t.Fatalf("height %d viewport height = %d, want at least 1", height, model.viewport.Height)
		}
	}
}

func TestTurnDoneUpdatesContextUsageStatus(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{status: Status{Model: "claude-test", PermissionMode: "safe"}})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model = updated.(Model)
	updated, _ = model.Update(turnDoneMsg{Result: TurnResult{Text: "first", InputTokens: 1234, OutputTokens: 56}})
	model = updated.(Model)
	updated, _ = model.Update(turnDoneMsg{Result: TurnResult{Text: "second", InputTokens: 2000, OutputTokens: 44}})
	model = updated.(Model)

	view := model.View()
	if !strings.Contains(view, "Ctx: total 3.2k in / 100 out") {
		t.Fatalf("view = %q, want cumulative usage context", view)
	}
	if strings.Contains(view, "%") || strings.Contains(view, "context used") {
		t.Fatalf("view = %q, should not show unsupported context percentage", view)
	}
}

func TestTurnDoneUpdatesThinkingStatus(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{status: Status{Model: "claude-test", PermissionMode: "safe"}})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model = updated.(Model)
	if !strings.Contains(model.View(), "Think: off") {
		t.Fatalf("view = %q, want default thinking off", model.View())
	}

	updated, _ = model.Update(turnDoneMsg{Result: TurnResult{Text: "done", ReasoningScenario: "architecture", ReasoningConfidence: "high"}})
	model = updated.(Model)
	view := model.View()
	if !strings.Contains(view, "Think: architecture high") {
		t.Fatalf("view = %q, want reasoning mode", view)
	}
	model.input.SetValue("/status")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if !strings.Contains(model.messages[len(model.messages)-1].Text, "think: architecture high") {
		t.Fatalf("status message = %q, want reasoning mode", model.messages[len(model.messages)-1].Text)
	}
}

func TestBottomStatusHidesUnknownGitAndDiff(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{status: Status{Model: "claude-test", PermissionMode: "safe"}})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(Model)

	view := model.View()
	if strings.Contains(view, "git ") || strings.Contains(view, "(+") || strings.Contains(view, "clean") {
		t.Fatalf("view = %q, should hide unavailable git and diff status", view)
	}
}

func TestBottomStatusTruncatesOnNarrowWidth(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{status: Status{Model: "claude-test", CWD: "/Users/secret/project", PermissionMode: "safe", SessionID: "sess_12345678901234567890"}})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	model = updated.(Model)

	view := model.View()
	if view == "" || !strings.Contains(view, "Model:") {
		t.Fatalf("view = %q, want compact bottom status", view)
	}
}

func TestBottomStatusDoesNotExposeFullCWDOrSessionPath(t *testing.T) {
	t.Parallel()

	longSession := "sess_12345678901234567890"
	model := New(&fakeService{status: Status{Model: "claude-test", CWD: "/Users/secret/project", PermissionMode: "safe", SessionID: longSession}})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model = updated.(Model)

	view := model.View()
	if strings.Contains(view, "/Users/secret/project") || strings.Contains(view, longSession) {
		t.Fatalf("view = %q, leaked full cwd or session id", view)
	}
	if !strings.Contains(view, "cwd project") || !strings.Contains(view, "sess_123456…") {
		t.Fatalf("view = %q, want cwd basename and short session id", view)
	}
}
