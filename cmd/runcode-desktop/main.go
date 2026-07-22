// Command runcode-desktop is the Wails shell for the runcode desktop app. It is a
// thin adapter: it supplies an EventSink backed by the Wails runtime, binds the
// transport-agnostic desktop.App to the frontend, and embeds the built web UI.
// All session logic lives in internal/desktop, which has no Wails dependency.
package main

import (
	"context"
	"embed"
	"log"
	"sync"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/wt68/runcode/internal/desktop"
)

//go:embed all:frontend/dist
var assets embed.FS

// eventSink forwards desktop events to the Wails frontend. The runtime context is
// only available from OnStartup; emits before then are dropped (no events fire
// that early).
type eventSink struct {
	mu  sync.RWMutex
	ctx context.Context
}

func (s *eventSink) setContext(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
}

func (s *eventSink) Emit(event string, data any) {
	s.mu.RLock()
	ctx := s.ctx
	s.mu.RUnlock()
	if ctx != nil {
		wruntime.EventsEmit(ctx, event, data)
	}
}

// wailsDialog implements desktop.Dialoger with Wails' native file dialogs. It
// shares the runtime context set at OnStartup.
type wailsDialog struct {
	mu  sync.RWMutex
	ctx context.Context
}

func (d *wailsDialog) setContext(ctx context.Context) {
	d.mu.Lock()
	d.ctx = ctx
	d.mu.Unlock()
}

func (d *wailsDialog) PickFile(title string) (string, error) {
	d.mu.RLock()
	ctx := d.ctx
	d.mu.RUnlock()
	if ctx == nil {
		return "", nil
	}
	return wruntime.OpenFileDialog(ctx, wruntime.OpenDialogOptions{
		Title:   title,
		Filters: []wruntime.FileFilter{{DisplayName: "SKILL.md / Markdown", Pattern: "SKILL.md;*.md"}},
	})
}

func (d *wailsDialog) PickFolder(title, defaultDir string) (string, error) {
	d.mu.RLock()
	ctx := d.ctx
	d.mu.RUnlock()
	if ctx == nil {
		return "", nil
	}
	return wruntime.OpenDirectoryDialog(ctx, wruntime.OpenDialogOptions{Title: title, DefaultDirectory: defaultDir})
}

func (d *wailsDialog) PickImage(title string) (string, error) {
	d.mu.RLock()
	ctx := d.ctx
	d.mu.RUnlock()
	if ctx == nil {
		return "", nil
	}
	return wruntime.OpenFileDialog(ctx, wruntime.OpenDialogOptions{
		Title:   title,
		Filters: []wruntime.FileFilter{{DisplayName: "图片 (png, jpg, gif, webp)", Pattern: "*.png;*.jpg;*.jpeg;*.gif;*.webp"}},
	})
}

func main() {
	sink := &eventSink{}
	dlg := &wailsDialog{}
	app := desktop.New(sink)
	app.SetDialoger(dlg)

	err := wails.Run(&options.App{
		Title: "XRUN",
		// Roomier default so the chat column stays comfortable with both the session
		// sidebar and the task-progress rail open; MinWidth keeps it usable if resized
		// down.
		Width:     1280,
		Height:    820,
		MinWidth:  1024,
		MinHeight: 680,
		// Hide the OS title bar; the frontend draws its own draggable title bar
		// with window controls.
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: func(ctx context.Context) {
			sink.setContext(ctx)
			dlg.setContext(ctx)
		},
		OnShutdown: func(context.Context) {
			_ = app.CloseSession()
		},
		// The frontend calls these via window.go.desktop.App.*
		Bind: []any{app},
	})
	if err != nil {
		log.Fatalf("runcode-desktop: %v", err)
	}
}
