//go:build kylin

// 银河麒麟 V10 专用外壳，跑在 Wails v2 上。
//
// 为什么要单独一份：V10 的 WebKitGTK 只有 4.0 这个 ABI（基础版 2.28.1，打过 2403
// 更新的是 2.38.6），而 Wails v3 只有 4.1 与 6.0 两条路径，连它的 gtk3 老路走的也是
// 4.1；v3 还调用了 webkit_web_view_evaluate_javascript，那是 WebKitGTK 2.40 才引入的
// 函数。Wails v2 相反：它**默认**就编译到 webkit2gtk-4.0，用的二十个 WebKit 函数
// 全是老接口（JS 执行走的是 run_javascript），所以 V10 上能编译能跑。
//
// 共用的东西比分开的多：引擎、internal/desktop 那一整套业务逻辑、以及整个前端都
// 原样复用，两份外壳只差这一个文件。构建走 -tags kylin，v3 那份在 main.go 上挂了
// !kylin。go.mod 同时 require v2 与 v3，实测无依赖冲突；只有被编进去的那一套会进
// 二进制。
//
// 与主线的三处功能差异，都是 v2 的模型决定的，不是没做完：
//
//  1. 单窗口。v2 没有多窗口，录音窗因此不建——而录音采集本来就只有 Windows 有
//     （internal/audio 在别处是空实现），所以 Linux 上不损失任何已有能力。
//     RecorderWindow 的三个方法照旧绑上去，一律返回「本平台不支持」，让前端按既有
//     的失败路径优雅降级，而不是调用一个不存在的方法。
//  2. 无边框窗口保留，标题栏仍由前端自绘。
//  3. 拖放文件：v2 靠 options 里的 DragAndDrop 开启，语义与 v3 的 EnableFileDrop 一致。
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"os"
	"sync"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/wt68/runcode/internal/desktop"
)

//go:embed all:frontend/dist
var kylinAssets embed.FS

// kylinSink 把桌面核心的事件转给前端。
//
// 与 v3 那版的关键差异：v2 的事件必须带 runtime ctx，而 ctx 要等 OnStartup 才拿得到。
// 在那之前发生的 emit 只能丢掉——这也是 v3 把 App 提前给出来所修掉的那个问题。
// 实践上无碍：核心要到 Startup 之后才开始产生事件。
type kylinSink struct {
	mu  sync.RWMutex
	ctx context.Context
}

func (s *kylinSink) setCtx(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
}

func (s *kylinSink) Emit(event string, data any) {
	s.mu.RLock()
	ctx := s.ctx
	s.mu.RUnlock()
	if ctx != nil {
		wruntime.EventsEmit(ctx, event, data)
	}
}

// kylinDialog 实现 desktop.Dialoger，用 v2 的原生对话框。
type kylinDialog struct {
	mu  sync.RWMutex
	ctx context.Context
}

func (d *kylinDialog) setCtx(ctx context.Context) {
	d.mu.Lock()
	d.ctx = ctx
	d.mu.Unlock()
}

func (d *kylinDialog) get() context.Context {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.ctx
}

func (d *kylinDialog) PickFile(title string) (string, error) {
	ctx := d.get()
	if ctx == nil {
		return "", nil
	}
	return wruntime.OpenFileDialog(ctx, wruntime.OpenDialogOptions{
		Title:   title,
		Filters: []wruntime.FileFilter{{DisplayName: "SKILL.md / Markdown", Pattern: "SKILL.md;*.md"}},
	})
}

func (d *kylinDialog) PickFolder(title, defaultDir string) (string, error) {
	ctx := d.get()
	if ctx == nil {
		return "", nil
	}
	return wruntime.OpenDirectoryDialog(ctx, wruntime.OpenDialogOptions{
		Title:            title,
		DefaultDirectory: defaultDir,
	})
}

func (d *kylinDialog) PickImage(title string) (string, error) {
	ctx := d.get()
	if ctx == nil {
		return "", nil
	}
	return wruntime.OpenFileDialog(ctx, wruntime.OpenDialogOptions{
		Title: title,
		Filters: []wruntime.FileFilter{
			{DisplayName: "图片 (png, jpg, gif, webp)", Pattern: "*.png;*.jpg;*.jpeg;*.gif;*.webp"},
		},
	})
}

// kylinQuit 实现 desktop.Quitter。版本更新装完要用它退出。
type kylinQuit struct {
	mu  sync.RWMutex
	ctx context.Context
}

func (q *kylinQuit) setCtx(ctx context.Context) {
	q.mu.Lock()
	q.ctx = ctx
	q.mu.Unlock()
}

func (q *kylinQuit) Quit() {
	q.mu.RLock()
	ctx := q.ctx
	q.mu.RUnlock()
	if ctx != nil {
		wruntime.Quit(ctx)
	}
}

// RecorderWindow 在这份外壳里是**明确的不支持**，不是没实现完。
//
// v2 只有一个窗口，开不出第二个;而录音采集在非 Windows 上本来就是空实现
// （internal/audio 的 stub_other.go），所以这里不损失任何原本可用的能力。
// 三个方法照旧绑给前端并返回错误——前端调用它们时走的是既有的失败提示路径，
// 比调用一个根本不存在的绑定方法（那会是一条看不懂的运行时错误）体面得多。
type RecorderWindow struct{}

var errNoRecorderWindow = errors.New("本平台不支持独立的录音窗")

func (r *RecorderWindow) Show() error          { return errNoRecorderWindow }
func (r *RecorderWindow) Hide() error          { return errNoRecorderWindow }
func (r *RecorderWindow) SetMode(string) error { return errNoRecorderWindow }

func main() {
	// 更新看门模式必须排在最前，理由同 v3 那份：它是同一个二进制的另一副面孔，
	// 一旦走到下面的单实例锁那里，轻则静默退出（没人拉起新版本），重则把用户真正
	// 那次启动挡在门外。
	if desktop.IsUpdateWatch(os.Args) {
		os.Exit(desktop.RunUpdateWatch(os.Args))
	}

	sink := &kylinSink{}
	dlg := &kylinDialog{}
	quit := &kylinQuit{}

	deskApp := desktop.New(sink)
	deskApp.SetDialoger(dlg)
	deskApp.SetQuitter(quit)
	// 采集实现只有 Windows 有，这里必然拿不到，录音入口会自己置灰。不调
	// SetCapturer 与调了一个空实现等价，少一层无谓的包装。

	dist, err := fs.Sub(kylinAssets, "frontend/dist")
	if err != nil {
		log.Fatalf("runcode-desktop: 定位前端产物: %v", err)
	}

	err = wails.Run(&options.App{
		Title:     brandTitle,
		Width:     1280,
		Height:    820,
		MinWidth:  1024,
		MinHeight: 680,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: dist,
		},
		// 单实例锁按品牌分，语义与 v3 那份一致：共用一把会让不同品牌互相挡住启动。
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: brandID,
		},
		// 拖文件进输入框。v2 这边靠它开启，对应 v3 的 EnableFileDrop；前端那套
		// data-file-drop-target 的判定是同一份代码，不用改。
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
		},
		Linux: &linux.Options{
			// 无边框窗口下仍要让窗口管理器按普通应用对待。
			WindowIsTranslucent: false,
			ProgramName:         brandTitle,
		},
		OnStartup: func(ctx context.Context) {
			sink.setCtx(ctx)
			dlg.setCtx(ctx)
			quit.setCtx(ctx)
			deskApp.Startup()
		},
		OnShutdown: func(context.Context) {
			deskApp.Shutdown()
			_ = deskApp.CloseAllSessions()
		},
		Bind: []any{
			deskApp,
			&RecorderWindow{},
		},
	})
	if err != nil {
		log.Fatalf("runcode-desktop: %v", err)
	}
}
