//go:build !kylin

package main

import (
	"os"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wt68/runcode/internal/desktop"
)

func createDesktopWindowLifecycle(
	app *application.App,
	mainWin *application.WebviewWindow,
	deskApp *desktop.App,
	title string,
) *desktop.WindowLifecycle {
	var life *desktop.WindowLifecycle
	restore := func() {
		if life != nil {
			_ = life.RestoreMainWindow()
		}
	}
	report := func(err error) {
		if err == nil {
			return
		}
		app.Dialog.Error().
			SetTitle("窗口操作失败").
			SetMessage(err.Error()).
			Show()
	}
	life = desktop.NewAppWindowLifecycle(deskApp, desktop.WindowHostOptions{
		BackgroundSupported: runtime.GOOS == "windows",
		Hide: func() error {
			mainWin.Hide()
			return nil
		},
		UnMinimise: mainWin.UnMinimise,
		Show:       func() { mainWin.Show() },
		Focus:      mainWin.Focus,
		ConfirmExit: func(prompt desktop.ExitConfirmation, decide func(bool)) {
			dialog := app.Dialog.Question().
				SetTitle("退出客户端").
				SetMessage(prompt.Message)
			yes, no, cancelLabel := questionButtonLabels(runtime.GOOS, "退出", "取消", "取消")
			cancel := dialog.AddButton(cancelLabel).OnClick(func() { decide(false) })
			dialog.AddButton(yes).OnClick(func() { decide(true) })
			if runtime.GOOS == "windows" {
				noButton := dialog.AddButton(no).OnClick(func() { decide(false) })
				dialog.SetDefaultButton(noButton)
			} else {
				dialog.SetDefaultButton(cancel)
			}
			dialog.SetCancelButton(cancel)
			dialog.Show()
		},
		ConfirmClose: func(prompt desktop.CloseConfirmation, decide func(desktop.CloseChoice)) {
			dialog := app.Dialog.Question().
				SetTitle("关闭客户端").
				SetMessage(prompt.Message)
			yes, no, cancelLabel := questionButtonLabels(runtime.GOOS, "隐藏主窗", "直接退出", "取消")
			hide := dialog.AddButton(yes).OnClick(func() { decide(desktop.CloseHide) })
			dialog.AddButton(no).OnClick(func() { decide(desktop.CloseExit) })
			cancel := dialog.AddButton(cancelLabel).OnClick(func() { decide(desktop.CloseCancel) })
			dialog.SetDefaultButton(hide)
			// 按 Esc 或点确认框的关闭按钮都取消本次操作，窗口和调度保持原状。
			dialog.SetCancelButton(cancel)
			dialog.Show()
		},
		Quit: app.Quit,
		ForceQuit: func() {
			if runtime.GOOS == "windows" {
				os.Exit(0)
			}
		},
		ReportError: report,
	})
	installAutomationTray(app, mainWin, life, title, restore)
	return life
}

// Wails v3 beta.9 的 Windows Question 对话框始终显示系统“是/否”按钮，
// 并按 MessageBox 返回值 Yes/No/Cancel 匹配回调，不使用自定义显示标签。
// Windows 必须注册这些英文内部标签，否则按钮看似可点但 decide 永远不会执行。
func questionButtonLabels(goos, primary, secondary, cancel string) (string, string, string) {
	if goos == "windows" {
		return "Yes", "No", "Cancel"
	}
	return primary, secondary, cancel
}
