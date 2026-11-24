package desktop

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/automation"
)

type lifecycleHarness struct {
	mu           sync.Mutex
	events       []string
	prompts      []ExitConfirmation
	closePrompts []CloseConfirmation
	hideErr      error
	summary      automation.ExitSummary
	summaryErr   error
	holdErr      error
	prepare      chan bool
	quit         chan struct{}
	forceQuit    chan struct{}
	reported     chan error
}

func newLifecycleHarness(t *testing.T) (*WindowLifecycle, *lifecycleHarness) {
	t.Helper()
	h := &lifecycleHarness{
		prepare:   make(chan bool, 1),
		quit:      make(chan struct{}, 1),
		forceQuit: make(chan struct{}, 1),
		reported:  make(chan error, 1),
	}
	life := NewWindowLifecycle(WindowLifecycleOptions{
		BackgroundSupported: true,
		Hide: func() error {
			h.record("hide")
			return h.hideErr
		},
		UnMinimise: func() { h.record("unminimise") },
		Show:       func() { h.record("show") },
		Focus:      func() { h.record("focus") },
		ConfirmExit: func(prompt ExitConfirmation, decide func(bool)) {
			h.mu.Lock()
			h.prompts = append(h.prompts, prompt)
			h.mu.Unlock()
			decide(false)
		},
		ConfirmClose: func(prompt CloseConfirmation, decide func(CloseChoice)) {
			h.mu.Lock()
			h.closePrompts = append(h.closePrompts, prompt)
			h.mu.Unlock()
			decide(CloseHide)
		},
		ExitSummary: func(context.Context) (automation.ExitSummary, error) {
			return h.summary, h.summaryErr
		},
		HoldAdmission: func(string) (func(), error) {
			h.record("hold")
			if h.holdErr != nil {
				return nil, h.holdErr
			}
			return func() { h.record("release") }, nil
		},
		PrepareExit: func(time.Duration) bool {
			h.record("prepare")
			h.prepare <- true
			return true
		},
		Quit: func() {
			h.record("quit")
			h.quit <- struct{}{}
		},
		ReportError: func(err error) { h.reported <- err },
	})
	return life, h
}

func (h *lifecycleHarness) record(event string) {
	h.mu.Lock()
	h.events = append(h.events, event)
	h.mu.Unlock()
}

func (h *lifecycleHarness) eventList() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.events...)
}

func (h *lifecycleHarness) promptList() []ExitConfirmation {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]ExitConfirmation(nil), h.prompts...)
}

func (h *lifecycleHarness) closePromptList() []CloseConfirmation {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]CloseConfirmation(nil), h.closePrompts...)
}

func TestWindowCloseHidesWithoutCleanup(t *testing.T) {
	life, h := newLifecycleHarness(t)
	if err := life.CloseMainWindow(); err != nil {
		t.Fatal(err)
	}
	if got := len(h.closePromptList()); got != 1 {
		t.Fatalf("close prompts = %d, want 1", got)
	}
	if got := h.eventList(); len(got) != 1 || got[0] != "hide" {
		t.Fatalf("events = %v", got)
	}
	if life.Quitting() {
		t.Fatal("closing the main window entered the quitting state")
	}
	select {
	case <-h.prepare:
		t.Fatal("window close started PrepareExit")
	case <-h.quit:
		t.Fatal("window close quit the app")
	default:
	}
}

func TestMainCloseActiveRunWarnsAndCanExit(t *testing.T) {
	life, h := newLifecycleHarness(t)
	h.summary = automation.ExitSummary{EnabledCount: 1, UnfinishedCount: 1}
	life.opts.ConfirmClose = func(prompt CloseConfirmation, decide func(CloseChoice)) {
		h.mu.Lock()
		h.closePrompts = append(h.closePrompts, prompt)
		h.mu.Unlock()
		decide(CloseExit)
	}
	if err := life.CloseMainWindow(); err != nil {
		t.Fatal(err)
	}
	prompts := h.closePromptList()
	if len(prompts) != 1 || !strings.Contains(prompts[0].Message, "自动化任务正在执行") {
		t.Fatalf("close prompts = %+v", prompts)
	}
	select {
	case <-h.prepare:
	case <-time.After(time.Second):
		t.Fatal("close-exit did not prepare cleanup")
	}
	select {
	case <-h.quit:
	case <-time.After(time.Second):
		t.Fatal("close-exit did not quit")
	}
	if !life.Quitting() {
		t.Fatal("close-exit did not mark quitting")
	}
}

func TestCloseConfirmationExplainsNativeYesNoFallback(t *testing.T) {
	prompt := closeConfirmation(automation.ExitSummary{EnabledCount: 2, UnfinishedCount: 1}, nil, MailExitSummary{}, nil)
	for _, want := range []string{"隐藏主窗/是", "直接退出/否", "取消", "正在执行"} {
		if !strings.Contains(prompt.Message, want) {
			t.Fatalf("close prompt %q does not explain %q", prompt.Message, want)
		}
	}
}

func TestMainCloseCancelChangesNothing(t *testing.T) {
	life, h := newLifecycleHarness(t)
	life.opts.ConfirmClose = func(prompt CloseConfirmation, decide func(CloseChoice)) {
		h.mu.Lock()
		h.closePrompts = append(h.closePrompts, prompt)
		h.mu.Unlock()
		decide(CloseCancel)
	}
	if err := life.CloseMainWindow(); err != nil {
		t.Fatal(err)
	}
	if len(h.closePromptList()) != 1 || life.Quitting() || len(h.eventList()) != 0 {
		t.Fatalf("cancel prompts=%d quitting=%v events=%v", len(h.closePromptList()), life.Quitting(), h.eventList())
	}
}

func TestWindowRestoreKeepsState(t *testing.T) {
	life, h := newLifecycleHarness(t)
	if err := life.RestoreMainWindow(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.eventList(), ","); got != "unminimise,show,focus" {
		t.Fatalf("restore order = %s", got)
	}
}

func TestExitConfirmationCounts(t *testing.T) {
	cases := []struct {
		name    string
		summary automation.ExitSummary
		err     error
		prompt  bool
	}{
		{name: "enabled", summary: automation.ExitSummary{EnabledCount: 1}, prompt: true},
		{name: "active paused task", summary: automation.ExitSummary{UnfinishedCount: 1}, prompt: true},
		{name: "allstopped", summary: automation.ExitSummary{}, prompt: false},
		{name: "unknown", err: errors.New("database unavailable"), prompt: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			life, h := newLifecycleHarness(t)
			h.summary = tc.summary
			h.summaryErr = tc.err
			if err := life.RequestAppExit(); err != nil {
				t.Fatal(err)
			}
			prompts := h.promptList()
			if tc.prompt {
				if len(prompts) != 1 {
					t.Fatalf("prompts = %d, want 1", len(prompts))
				}
				if !strings.Contains(prompts[0].Message, "停止后台自动化任务") {
					t.Fatalf("prompt = %q", prompts[0].Message)
				}
				if tc.err != nil && !strings.Contains(prompts[0].Message, "无法确认后台任务状态") {
					t.Fatalf("unknown-summary prompt = %q", prompts[0].Message)
				}
				return
			}
			if len(prompts) != 0 {
				t.Fatalf("unexpected prompt: %+v", prompts)
			}
			select {
			case <-h.prepare:
			case <-time.After(time.Second):
				t.Fatal("no-prompt exit did not prepare cleanup")
			}
			select {
			case <-h.quit:
			case <-time.After(time.Second):
				t.Fatal("no-prompt exit did not quit")
			}
		})
	}
}

func TestExitConfirmationExplainsNativeYesNoFallback(t *testing.T) {
	prompt := exitConfirmation(automation.ExitSummary{EnabledCount: 2, UnfinishedCount: 1}, nil, MailExitSummary{}, nil)
	for _, want := range []string{"退出/是", "取消/否", "关闭窗口", "已开启任务"} {
		if !strings.Contains(prompt.Message, want) {
			t.Fatalf("exit prompt %q does not explain %q", prompt.Message, want)
		}
	}
}

func TestExitConfirmationMailPendingAlone(t *testing.T) {
	life, h := newLifecycleHarness(t)
	life.opts.MailExitSummary = func(context.Context) (MailExitSummary, error) {
		return MailExitSummary{PendingCount: 3}, nil
	}
	if err := life.RequestAppExit(); err != nil {
		t.Fatal(err)
	}
	prompts := h.promptList()
	if len(prompts) != 1 || !strings.Contains(prompts[0].Message, "待发邮件：3") {
		t.Fatalf("prompts=%+v", prompts)
	}
}

func TestExitConfirmationMailAndAutomationCounts(t *testing.T) {
	prompt := exitConfirmation(
		automation.ExitSummary{EnabledCount: 2, UnfinishedCount: 1}, nil,
		MailExitSummary{PendingCount: 4}, nil,
	)
	for _, want := range []string{"已开启任务：2", "未完成运行：1", "待发邮件：4"} {
		if !strings.Contains(prompt.Message, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt.Message)
		}
	}
}

func TestExitCancelKeepsScheduler(t *testing.T) {
	life, h := newLifecycleHarness(t)
	h.summary.EnabledCount = 1
	if err := life.RequestAppExit(); err != nil {
		t.Fatal(err)
	}
	if len(h.promptList()) != 1 {
		t.Fatal("expected confirmation")
	}
	if life.Quitting() {
		t.Fatal("cancel set quitting")
	}
	for _, event := range h.eventList() {
		if event == "hold" || event == "prepare" || event == "quit" {
			t.Fatalf("cancel changed lifecycle: %v", h.eventList())
		}
	}
}

func TestExitDoubleClickOneDialog(t *testing.T) {
	life, h := newLifecycleHarness(t)
	h.summary.EnabledCount = 1
	decide := make(chan func(bool), 1)
	life.opts.ConfirmExit = func(prompt ExitConfirmation, cb func(bool)) {
		h.mu.Lock()
		h.prompts = append(h.prompts, prompt)
		h.mu.Unlock()
		decide <- cb
	}
	if err := life.RequestAppExit(); err != nil {
		t.Fatal(err)
	}
	if err := life.RequestAppExit(); err != nil {
		t.Fatal(err)
	}
	if got := len(h.promptList()); got != 1 {
		t.Fatalf("dialogs = %d, want 1", got)
	}
	(<-decide)(false)
}

func TestExitAdmissionRace(t *testing.T) {
	life, h := newLifecycleHarness(t)
	h.summary.EnabledCount = 1
	life.opts.ConfirmExit = func(_ ExitConfirmation, decide func(bool)) { decide(true) }
	if err := life.RequestAppExit(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.prepare:
	case <-time.After(time.Second):
		t.Fatal("confirmed exit did not prepare cleanup")
	}
	select {
	case <-h.quit:
	case <-time.After(time.Second):
		t.Fatal("confirmed exit did not quit")
	}
	if got := strings.Join(h.eventList(), ","); got != "hold,prepare,quit" {
		t.Fatalf("exit order = %s", got)
	}
	if !life.Quitting() {
		t.Fatal("confirmed exit was not marked quitting")
	}
}

func TestExitHoldFailureKeepsProcessAlive(t *testing.T) {
	life, h := newLifecycleHarness(t)
	h.summary.EnabledCount = 1
	h.holdErr = errors.New("service unavailable")
	life.opts.ConfirmExit = func(_ ExitConfirmation, decide func(bool)) { decide(true) }
	if err := life.RequestAppExit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-h.reported:
		if !strings.Contains(err.Error(), "停止新任务派发") {
			t.Fatalf("reported = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("hold failure was not reported")
	}
	if life.Quitting() {
		t.Fatal("hold failure still marked quitting")
	}
	select {
	case <-h.quit:
		t.Fatal("hold failure quit the process")
	default:
	}
}

func TestQuitFallbackWhenEventLoopDoesNotExit(t *testing.T) {
	life, h := newLifecycleHarness(t)
	life.opts.QuitFallbackDelay = 20 * time.Millisecond
	life.opts.ForceQuit = func() {
		h.record("forcequit")
		h.forceQuit <- struct{}{}
	}
	if err := life.RequestAppExit(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.prepare:
	case <-time.After(time.Second):
		t.Fatal("exit did not prepare cleanup")
	}
	select {
	case <-h.quit:
	case <-time.After(time.Second):
		t.Fatal("exit did not invoke Quit")
	}
	select {
	case <-h.forceQuit:
	case <-time.After(time.Second):
		t.Fatal("nonzero process survived without the forced-exit fallback")
	}
	if got := strings.Join(h.eventList(), ","); got != "hold,prepare,quit,forcequit" {
		t.Fatalf("exit fallback order = %s", got)
	}
}

func TestConfirmedExitQuitsAfterCleanupTimeout(t *testing.T) {
	life, h := newLifecycleHarness(t)
	life.opts.PrepareExit = func(time.Duration) bool {
		h.record("prepare-timeout")
		h.prepare <- true
		return false
	}
	if err := life.RequestAppExit(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.prepare:
	case <-time.After(time.Second):
		t.Fatal("exit did not prepare cleanup")
	}
	select {
	case <-h.quit:
	case <-time.After(time.Second):
		t.Fatal("cleanup timeout prevented the host Quit call")
	}
	if got := strings.Join(h.eventList(), ","); got != "hold,prepare-timeout,quit" {
		t.Fatalf("timeout exit order = %s", got)
	}
}
