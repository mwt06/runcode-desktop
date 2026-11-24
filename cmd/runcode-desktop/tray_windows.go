//go:build windows && !kylin

package main

import (
	"context"
	_ "embed"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wt68/runcode/internal/desktop"
)

//go:embed build/appicon.png
var brandedTrayIcon []byte

func installAutomationTray(
	app *application.App,
	_ *application.WebviewWindow,
	life *desktop.WindowLifecycle,
	title string,
	restore func(),
) {
	tray := app.SystemTray.New()
	menu := app.NewMenu()
	menu.Add("打开" + title).OnClick(func(*application.Context) { restore() })
	pending := menu.Add("查看待处理任务").OnClick(func(*application.Context) {
		restore()
		app.Event.Emit("automation:open_manager", nil)
	})
	pending.SetHidden(true)
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(*application.Context) {
		if err := life.RequestAppExit(); err != nil {
			app.Dialog.Error().SetTitle("无法退出").SetMessage(err.Error()).Show()
		}
	})
	tray.SetIcon(brandedTrayIcon)
	tray.SetMenu(menu)
	tray.SetTooltip(title)
	tray.
		OnClick(restore).
		OnDoubleClick(restore).
		OnRightClick(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			count, _ := life.PendingCount(ctx)
			cancel()
			pending.SetHidden(count <= 0)
			tray.OpenMenu()
		})
}
