package desktop

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wt68/runcode/internal/automation"
)

const (
	exitSummaryTimeout      = 5 * time.Second
	defaultQuitFallbackWait = 2 * time.Second
)

// ExitConfirmation is the native prompt data prepared before any cleanup starts.
// SummaryKnown is false when the task database could not be queried.
type ExitConfirmation struct {
	EnabledCount    int
	UnfinishedCount int
	SummaryKnown    bool
	Message         string
}

// CloseChoice is the user's response to the main-window close chooser.
type CloseChoice string

const (
	// CloseCancel keeps the main window visible and leaves scheduling untouched.
	CloseCancel CloseChoice = "cancel"
	// CloseHide hides only the main window and leaves the tray process running.
	CloseHide CloseChoice = "hide"
	// CloseExit uses the same bounded cleanup pipeline as the tray Exit action.
	CloseExit CloseChoice = "exit"
)

// CloseConfirmation is shown for every main-window X press on a background-capable host.
type CloseConfirmation struct {
	EnabledCount    int
	UnfinishedCount int
	SummaryKnown    bool
	Message         string
}

// MailExitSummary 是邮件独立退出摘要；不复用 automation.ExitSummary 字段。
type MailExitSummary struct {
	PendingCount int
}

// WindowLifecycleOptions are the platform-neutral lifecycle seams. The Wails
// shell supplies window operations, native confirmation and process quit; the
// scheduler/exit callbacks stay testable without a GUI.
type WindowLifecycleOptions struct {
	BackgroundSupported bool
	Hide                func() error
	UnMinimise          func()
	Show                func()
	Focus               func()
	ConfirmExit         func(ExitConfirmation, func(bool))
	ConfirmClose        func(CloseConfirmation, func(CloseChoice))
	ExitSummary         func(context.Context) (automation.ExitSummary, error)
	// MailExitSummary 可选；nil 保持原 automation-only 行为。
	MailExitSummary   func(context.Context) (MailExitSummary, error)
	HoldAdmission     func(reason string) (release func(), err error)
	PrepareExit       func(time.Duration) bool
	Quit              func()
	ForceQuit         func()
	QuitFallbackDelay time.Duration
	ReportError       func(error)
}

// WindowHostOptions are the host-only half used by NewAppWindowLifecycle.
type WindowHostOptions struct {
	BackgroundSupported bool
	Hide                func() error
	UnMinimise          func()
	Show                func()
	Focus               func()
	ConfirmExit         func(ExitConfirmation, func(bool))
	ConfirmClose        func(CloseConfirmation, func(CloseChoice))
	Quit                func()
	ForceQuit           func()
	ReportError         func(error)
}

// WindowLifecycle implements the native close/exit policy. Closing a supported
// background host only hides the window. A real exit asks before holding
// scheduler admission, then runs the shared bounded cleanup and quits.
type WindowLifecycle struct {
	mu         sync.Mutex
	opts       WindowLifecycleOptions
	confirming bool
	quitting   bool
	hold       func()
}

// NewWindowLifecycle builds the platform-neutral close and exit controller.
func NewWindowLifecycle(opts WindowLifecycleOptions) *WindowLifecycle {
	if opts.QuitFallbackDelay <= 0 {
		opts.QuitFallbackDelay = defaultQuitFallbackWait
	}
	return &WindowLifecycle{opts: opts}
}

// NewAppWindowLifecycle wires the native host to one desktop App. This factory
// can access private scheduler/exit methods without exposing them as frontend
// commands.
func NewAppWindowLifecycle(app *App, host WindowHostOptions) *WindowLifecycle {
	return NewWindowLifecycle(WindowLifecycleOptions{
		BackgroundSupported: host.BackgroundSupported,
		Hide:                host.Hide,
		UnMinimise:          host.UnMinimise,
		Show:                host.Show,
		Focus:               host.Focus,
		ConfirmExit:         host.ConfirmExit,
		ConfirmClose:        host.ConfirmClose,
		ExitSummary:         app.automationExitSummary,
		MailExitSummary:     app.mailExitSummary,
		HoldAdmission: func(reason string) (func(), error) {
			autoRel, autoErr := app.holdAutomationAdmission(reason)
			mailRel := app.holdMailAdmission(reason)
			if autoErr != nil {
				if mailRel != nil {
					mailRel()
				}
				return nil, autoErr
			}
			return func() {
				if autoRel != nil {
					autoRel()
				}
				if mailRel != nil {
					mailRel()
				}
			}, nil
		},
		PrepareExit: func(limit time.Duration) bool { return app.PrepareExit(limit) },
		Quit:        host.Quit,
		ForceQuit:   host.ForceQuit,
		ReportError: host.ReportError,
	})
}

// BackgroundSupported reports whether closing may hide instead of exit.
func (l *WindowLifecycle) BackgroundSupported() bool {
	return l != nil && l.opts.BackgroundSupported
}

// Quitting reports whether bounded process cleanup has started.
func (l *WindowLifecycle) Quitting() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.quitting
}

// CloseMainWindow asks the user to hide, exit or cancel the close operation.
func (l *WindowLifecycle) CloseMainWindow() error {
	if l == nil {
		return errors.New("窗口生命周期不可用")
	}
	if !l.opts.BackgroundSupported {
		return l.RequestAppExit()
	}
	l.mu.Lock()
	if l.quitting || l.confirming {
		l.mu.Unlock()
		return nil
	}
	l.confirming = true
	l.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), exitSummaryTimeout)
	summary, summaryErr := automation.ExitSummary{}, error(nil)
	if l.opts.ExitSummary != nil {
		summary, summaryErr = l.opts.ExitSummary(ctx)
	}
	mailSum, mailErr := MailExitSummary{}, error(nil)
	if l.opts.MailExitSummary != nil {
		mailSum, mailErr = l.opts.MailExitSummary(ctx)
	}
	cancel()
	if l.opts.ConfirmClose == nil {
		l.cancelConfirmation()
		return errors.New("原生关闭确认不可用")
	}
	l.opts.ConfirmClose(closeConfirmation(summary, summaryErr, mailSum, mailErr), l.finishClose)
	return nil
}

func closeConfirmation(summary automation.ExitSummary, summaryErr error, mailSum MailExitSummary, mailErr error) CloseConfirmation {
	known := summaryErr == nil && mailErr == nil
	message := "请选择关闭方式：\n\n" +
		"“隐藏主窗/是”：隐藏主窗口，后台自动化任务继续运行。\n" +
		"“直接退出/否”：结束客户端，并停止后台自动化任务。\n" +
		"“取消”、Esc 或关闭提示框：取消本次操作，继续使用客户端。"
	switch {
	case summaryErr != nil:
		message += "\n\n无法确认后台任务状态；直接退出可能中断正在执行的自动化任务。"
	case summary.UnfinishedCount > 0:
		message += fmt.Sprintf(
			"\n\n自动化任务正在执行：%d 个。直接退出会中断当前执行。",
			summary.UnfinishedCount,
		)
	case summary.EnabledCount > 0:
		message += fmt.Sprintf(
			"\n\n当前有 %d 个已开启任务；直接退出后这些任务将停止。",
			summary.EnabledCount,
		)
	default:
		message += "\n\n当前没有正在执行或等待执行的自动化任务。"
	}
	message += mailExitHint(mailSum, mailErr)
	return CloseConfirmation{
		EnabledCount: summary.EnabledCount, UnfinishedCount: summary.UnfinishedCount,
		SummaryKnown: known, Message: message,
	}
}

func mailExitHint(mailSum MailExitSummary, mailErr error) string {
	switch {
	case mailErr != nil:
		return "\n\n无法确认待发邮件状态；直接退出可能中断邮件投递。"
	case mailSum.PendingCount > 0:
		return fmt.Sprintf("\n\n待发邮件：%d 封；直接退出会停止投递。", mailSum.PendingCount)
	default:
		return ""
	}
}

func (l *WindowLifecycle) finishClose(choice CloseChoice) {
	l.mu.Lock()
	if !l.confirming {
		l.mu.Unlock()
		return
	}
	if choice != CloseExit {
		l.confirming = false
	}
	l.mu.Unlock()
	switch choice {
	case CloseExit:
		// Re-enter the common confirmation state and suppress the second prompt:
		// the close chooser has already made the destructive choice explicit.
		l.finishExit(true)
	case CloseHide:
		if l.opts.Hide == nil {
			l.report(errors.New("无法隐藏主窗口"))
			return
		}
		if err := l.opts.Hide(); err != nil {
			_ = l.RestoreMainWindow()
			l.report(fmt.Errorf("隐藏主窗口失败: %w", err))
		}
	case CloseCancel:
	}
}

// RestoreMainWindow unminimises, shows and focuses the main window.
func (l *WindowLifecycle) RestoreMainWindow() error {
	if l == nil {
		return errors.New("窗口生命周期不可用")
	}
	if l.opts.UnMinimise == nil || l.opts.Show == nil || l.opts.Focus == nil {
		return errors.New("窗口恢复接口不可用")
	}
	l.opts.UnMinimise()
	l.opts.Show()
	l.opts.Focus()
	return nil
}

// PendingCount is used by the tray menu. Only unfinished runs make the
// "查看待处理任务" entry visible.
func (l *WindowLifecycle) PendingCount(ctx context.Context) (int, error) {
	if l == nil || l.opts.ExitSummary == nil {
		return 0, errors.New("无法查询后台任务状态")
	}
	summary, err := l.opts.ExitSummary(ctx)
	if err != nil {
		return 0, err
	}
	return summary.UnfinishedCount, nil
}

// RequestAppExit confirms and performs bounded process shutdown.
func (l *WindowLifecycle) RequestAppExit() error {
	if l == nil {
		return errors.New("窗口生命周期不可用")
	}
	l.mu.Lock()
	if l.quitting || l.confirming {
		l.mu.Unlock()
		return nil
	}
	l.confirming = true
	l.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), exitSummaryTimeout)
	summary, summaryErr := automation.ExitSummary{}, error(nil)
	if l.opts.ExitSummary != nil {
		summary, summaryErr = l.opts.ExitSummary(ctx)
	}
	mailSum, mailErr := MailExitSummary{}, error(nil)
	if l.opts.MailExitSummary != nil {
		mailSum, mailErr = l.opts.MailExitSummary(ctx)
	}
	cancel()
	needsConfirmation := summaryErr != nil || summary.EnabledCount > 0 || summary.UnfinishedCount > 0 ||
		mailErr != nil || mailSum.PendingCount > 0
	if !needsConfirmation {
		l.finishExit(true)
		return nil
	}
	if l.opts.ConfirmExit == nil {
		l.cancelConfirmation()
		return errors.New("原生退出确认不可用")
	}
	l.opts.ConfirmExit(exitConfirmation(summary, summaryErr, mailSum, mailErr), l.finishExit)
	return nil
}

func exitConfirmation(summary automation.ExitSummary, summaryErr error, mailSum MailExitSummary, mailErr error) ExitConfirmation {
	known := summaryErr == nil && mailErr == nil
	message := "请选择退出方式：\n\n" +
		"“退出/是”：结束客户端并停止后台自动化任务。\n" +
		"“取消/否”、Esc 或关闭窗口：取消退出，后台任务继续运行。"
	if summaryErr == nil {
		message += fmt.Sprintf(
			"\n\n已开启任务：%d 个；未完成运行：%d 个。",
			summary.EnabledCount, summary.UnfinishedCount,
		)
	} else {
		message += "\n\n无法确认后台任务状态；退出可能中断正在执行的自动化任务。"
	}
	message += mailExitHint(mailSum, mailErr)
	return ExitConfirmation{
		EnabledCount: summary.EnabledCount, UnfinishedCount: summary.UnfinishedCount,
		SummaryKnown: known, Message: message,
	}
}

func (l *WindowLifecycle) finishExit(confirmed bool) {
	l.mu.Lock()
	if !l.confirming {
		l.mu.Unlock()
		return
	}
	if !confirmed {
		l.confirming = false
		l.mu.Unlock()
		return
	}
	if l.quitting {
		l.confirming = false
		l.mu.Unlock()
		return
	}
	if l.opts.HoldAdmission == nil {
		l.confirming = false
		l.mu.Unlock()
		l.report(errors.New("无法停止新任务派发"))
		return
	}
	release, err := l.opts.HoldAdmission("exit")
	if err != nil {
		l.confirming = false
		l.mu.Unlock()
		l.report(fmt.Errorf("无法停止新任务派发: %w", err))
		return
	}
	if l.opts.PrepareExit == nil || l.opts.Quit == nil {
		if release != nil {
			release()
		}
		l.confirming = false
		l.mu.Unlock()
		l.report(errors.New("退出收尾接口不可用"))
		return
	}
	l.hold = release
	l.confirming = false
	l.quitting = true
	l.mu.Unlock()

	go func() {
		l.opts.PrepareExit(ExitTimeout)
		l.opts.Quit()
		// Wails 的 Quit 返回后事件循环理论上应立刻结束。若宿主仍存活，
		// 2 秒后改由外壳强退，避免隐藏窗口和托盘继续扣住单实例进程。
		if l.opts.ForceQuit != nil {
			time.AfterFunc(l.opts.QuitFallbackDelay, l.opts.ForceQuit)
		}
	}()
}

func (l *WindowLifecycle) cancelConfirmation() {
	l.mu.Lock()
	l.confirming = false
	l.mu.Unlock()
}

func (l *WindowLifecycle) report(err error) {
	if err != nil && l.opts.ReportError != nil {
		l.opts.ReportError(err)
	}
}

func (a *App) automationExitSummary(ctx context.Context) (automation.ExitSummary, error) {
	svc := a.automationService()
	if svc == nil {
		return automation.ExitSummary{}, errAutomationUnavailable
	}
	return svc.ExitSummary(ctx)
}

func (a *App) holdAutomationAdmission(reason string) (func(), error) {
	svc := a.automationService()
	if svc == nil {
		return nil, errAutomationUnavailable
	}
	return svc.HoldAdmission(reason), nil
}

func (a *App) automationBackgroundSupported() bool {
	a.autoCtl.mu.Lock()
	w := a.autoCtl.window
	a.autoCtl.mu.Unlock()
	return w != nil && w.BackgroundSupported()
}
