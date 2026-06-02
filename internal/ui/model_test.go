package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type fakeService struct {
	status     Status
	resetCount int
	closeCount int
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

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyUp})
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
