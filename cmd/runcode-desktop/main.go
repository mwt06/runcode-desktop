// Command runcode-desktop is the Wails shell for the runcode desktop app. It is a
// thin adapter: it supplies an EventSink backed by the Wails runtime, binds the
// transport-agnostic desktop.App to the frontend, and embeds the built web UI.
// All session logic lives in internal/desktop, which has no Wails dependency.
//
// 本文件是 Wails v3。相对 v2 的三处变化：
//   - wails.Run(&options.App{}) → application.New(...) + app.Run()
//   - Bind: []any{app} → Services: []application.Service{...}
//   - 事件与对话框不再需要到处传 runtime ctx，直接挂在 *application.App 上
//
// 换 v3 只为一件事：**原生多窗口**。设计稿要一个浮在所有应用之上的录音窗
// （用户切到腾讯会议后仍要看得见并能点结束），v2 全程只有一个窗口，做不到。
package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/wt68/runcode/cmd/runcode-desktop/internal/audio"
	"github.com/wt68/runcode/internal/desktop"
)

//go:embed all:frontend/dist
var assets embed.FS

// brandTitle is the OS window title (taskbar / alt-tab; the frameless window has
// no visible title bar — the frontend draws its own brand). It defaults to the
// original and is overridden at build time to match the frontend brand, e.g.:
//
//	wails3 build -ldflags "-X main.brandTitle=智开"
//
// alongside VITE_BRAND=zhikai for the frontend. See frontend/src/core/brand.ts,
// which is the source of truth for the in-app brand.
var brandTitle = "XRUN"

// 两个窗口的名字。v3 用名字定位窗口（app.Window.GetByName），是跨窗口操作的句柄。
const (
	windowMain     = "main"
	windowRecorder = "recorder"
)

// 录音窗的两个形态。大窗是设计稿里那个「新录音 N」工作区；浮窗是缩到右下角、
// 浮在所有应用之上的那个小条——它存在的全部理由就是用户切走之后还能看见。
const (
	recorderWideW, recorderWideH = 880, 560
	recorderMiniW, recorderMiniH = 320, 148
	recorderMiniMargin           = 16
)

// eventSink forwards desktop events to the Wails frontend.
//
// v3 下不再需要缓存 runtime ctx：事件从 *application.App 上发，而 App 在
// application.New 返回时就已可用。v2 那版必须等 OnStartup 拿到 ctx，期间的
// emit 只能丢掉。
type eventSink struct {
	mu  sync.RWMutex
	app *application.App
}

func (s *eventSink) setApp(app *application.App) {
	s.mu.Lock()
	s.app = app
	s.mu.Unlock()
}

func (s *eventSink) Emit(event string, data any) {
	s.mu.RLock()
	app := s.app
	s.mu.RUnlock()
	if app != nil {
		app.Event.Emit(event, data)
	}
}

// wailsDialog implements desktop.Dialoger with Wails' native file dialogs.
type wailsDialog struct {
	mu  sync.RWMutex
	app *application.App
}

func (d *wailsDialog) setApp(app *application.App) {
	d.mu.Lock()
	d.app = app
	d.mu.Unlock()
}

func (d *wailsDialog) get() *application.App {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.app
}

func (d *wailsDialog) PickFile(title string) (string, error) {
	app := d.get()
	if app == nil {
		return "", nil
	}
	return app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:          title,
		CanChooseFiles: true,
		Filters:        []application.FileFilter{{DisplayName: "SKILL.md / Markdown", Pattern: "SKILL.md;*.md"}},
	}).PromptForSingleSelection()
}

func (d *wailsDialog) PickFolder(title, defaultDir string) (string, error) {
	app := d.get()
	if app == nil {
		return "", nil
	}
	return app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:                title,
		Directory:            defaultDir,
		CanChooseDirectories: true,
		CanChooseFiles:       false,
	}).PromptForSingleSelection()
}

func (d *wailsDialog) PickImage(title string) (string, error) {
	app := d.get()
	if app == nil {
		return "", nil
	}
	return app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:          title,
		CanChooseFiles: true,
		Filters: []application.FileFilter{
			{DisplayName: "图片 (png, jpg, gif, webp)", Pattern: "*.png;*.jpg;*.jpeg;*.gif;*.webp"},
		},
	}).PromptForSingleSelection()
}

// RecorderWindow 是录音窗的控制面，作为一个独立服务绑给前端。
//
// 为什么不挂在 desktop.App 上：窗口控制天生是 Wails 的东西，而 internal/desktop
// 对 Wails 零依赖是整个分层的地基（也正是这次 v3 迁移面能压到一个文件的原因）。
// 真要让 desktop.App 能开关录音窗，走 Dialoger 那个模式——在 desktop 里定接口，
// 由本文件注入实现。
type RecorderWindow struct {
	app *application.App
}

// Show 显示录音窗（大窗形态）。
func (r *RecorderWindow) Show() error {
	w, ok := r.app.Window.GetByName(windowRecorder)
	if !ok {
		return fmt.Errorf("录音窗不存在")
	}
	w.SetSize(recorderWideW, recorderWideH)
	w.Center()
	w.Show()
	// 这里**不能**跟着调 w.Focus()。
	//
	// v3.0.0-beta.9 实测：对一个以 Hidden 创建、尚未显示过的窗口调 Focus()
	// 会崩掉整个进程——它的 WebView2 controller 还没建好，focus 路径上
	// edge.(*Chromium).GetController() 解引用空指针
	// （webview_window_windows.go:1110 → chromium.go:781）。
	//
	// 窗口本身是 AlwaysOnTop，Show 出来就在最前，不聚焦也看得见、点得到。
	// 等 v3 修掉、或找到可靠的「controller 已就绪」判据，再把聚焦加回来。
	return nil
}

// Hide 收起录音窗。
func (r *RecorderWindow) Hide() error {
	w, ok := r.app.Window.GetByName(windowRecorder)
	if !ok {
		return fmt.Errorf("录音窗不存在")
	}
	w.Hide()
	return nil
}

// SetMode 在大窗与右下角浮窗之间切换。
//
// 两个形态是**同一个窗口**改尺寸和位置，不是两个窗口——这样录音状态、WebView
// 里的计时器和实时字幕都不会因为切换而重建。
func (r *RecorderWindow) SetMode(mode string) error {
	w, ok := r.app.Window.GetByName(windowRecorder)
	if !ok {
		return fmt.Errorf("录音窗不存在")
	}
	switch mode {
	case "wide":
		w.SetSize(recorderWideW, recorderWideH)
		w.Center()
	case "mini":
		w.SetSize(recorderMiniW, recorderMiniH)
		// 贴右下角。用工作区尺寸而不是屏幕全尺寸，免得被任务栏盖住。
		if screen, err := w.GetScreen(); err == nil && screen != nil {
			wa := screen.WorkArea
			w.SetPosition(
				wa.X+wa.Width-recorderMiniW-recorderMiniMargin,
				wa.Y+wa.Height-recorderMiniH-recorderMiniMargin,
			)
		}
	default:
		return fmt.Errorf("未知的录音窗形态 %q（可用：wide、mini）", mode)
	}
	w.Show()
	return nil
}

// devtoolsArgs 在设了 RUNCODE_DEVTOOLS_PORT 时打开 WebView2 的远程调试端口。
//
// 默认关闭，而且必须保持默认关闭：端口一开，本机任何进程都能通过 CDP 在页面里执行
// 任意 JS —— 等于能调用全部 Go 绑定方法、读到登录令牌。它只该在开发机上临时打开
// （自检、排查 v3 绑定层的问题），不能变成分发包的默认行为。
func devtoolsArgs() []string {
	port := strings.TrimSpace(os.Getenv("RUNCODE_DEVTOOLS_PORT"))
	if port == "" {
		return nil
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		log.Printf("runcode-desktop: 忽略 RUNCODE_DEVTOOLS_PORT=%q（不是合法端口）", port)
		return nil
	}
	log.Printf("runcode-desktop: WebView2 远程调试已开在 :%s", port)
	return []string{"--remote-debugging-port=" + port}
}

func main() {
	sink := &eventSink{}
	dlg := &wailsDialog{}
	deskApp := desktop.New(sink)
	deskApp.SetDialoger(dlg)

	// 采集实现要 cgo 且只有 Windows 有，所以住在外壳里注进去（同 Dialoger）。
	// 拿不到不算致命：录音入口会以「当前系统不支持」置灰，其余功能照常。
	if capt, err := audio.New(); err != nil {
		log.Printf("runcode-desktop: 录音采集不可用: %v", err)
	} else {
		deskApp.SetCapturer(capt)
	}

	// 嵌进来的是 frontend/dist 整棵子树，要把前缀剥掉再交给 asset server。
	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatalf("runcode-desktop: 定位前端产物: %v", err)
	}

	recWin := &RecorderWindow{}

	app := application.New(application.Options{
		Name: brandTitle,
		Services: []application.Service{
			application.NewService(deskApp),
			application.NewService(recWin),
		},
		Assets: application.AssetOptions{
			// Bundled 版会顺带在 /wails/runtime.js 提供前端要的运行时。
			Handler: application.BundledAssetFileServer(dist),
		},
		// 桌面只允许开一个实例：两个进程同时开会抢同一份会话记录与配置。
		SingleInstance: &application.SingleInstanceOptions{UniqueID: "cn.ouconline.ai.runcode"},
		OnShutdown: func() {
			deskApp.Shutdown()
			_ = deskApp.CloseSession()
		},
		Windows: application.WindowsOptions{AdditionalBrowserArgs: devtoolsArgs()},
	})
	sink.setApp(app)
	dlg.setApp(app)
	recWin.app = app

	// 主窗。无边框，标题栏由前端自绘。
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:  windowMain,
		Title: brandTitle,
		URL:   "/",
		// Roomier default so the chat column stays comfortable with both the session
		// sidebar and the task-progress rail open; MinWidth keeps it usable if resized
		// down.
		Width: 1280, Height: 820,
		MinWidth: 1024, MinHeight: 680,
		Frameless: true,
	})

	// 录音窗。先建好但不显示——建窗要几百毫秒，等用户点「录音纪要」再建的话，
	// 那几百毫秒正好卡在他最想立刻看到反馈的时刻。
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:  windowRecorder,
		Title: "录音纪要",
		URL:   "/recorder.html",
		Width: recorderWideW, Height: recorderWideH,
		Frameless:   true,
		AlwaysOnTop: true,
		Hidden:      true,
	})

	deskApp.Startup()

	if err := app.Run(); err != nil {
		log.Fatalf("runcode-desktop: %v", err)
	}
}
