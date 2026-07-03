package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wt68/runcode/pkg/llm"
)

// maxImageBytes bounds a single attached image so a huge file cannot blow up the
// request payload (and the provider's limits). 8 MiB comfortably covers screenshots
// and photos while rejecting accidental multi-hundred-MB files.
const maxImageBytes = 8 << 20

// PickImageAttachment opens a native image picker and returns the chosen path ("" if
// cancelled). The frontend adds it to the composer's attachment list; the bytes are
// read at send time by SendMessageWithImages.
func (a *App) PickImageAttachment() (string, error) {
	a.mu.Lock()
	dialog := a.dialog
	a.mu.Unlock()
	if dialog == nil {
		return "", errors.New("当前环境不支持文件选择")
	}
	return dialog.PickImage("选择图片附件")
}

// SendMessageWithImages runs one user turn whose message carries the images at the
// given paths. It mirrors SendMessage (async, single in-flight turn) but reads the
// images and delegates to RunTurnWithImages. With no paths it falls back to a plain
// text turn.
func (a *App) SendMessageWithImages(text string, paths []string) error {
	if len(paths) == 0 {
		return a.SendMessage(text)
	}
	a.mu.Lock()
	if a.session == nil {
		a.mu.Unlock()
		return errNoSession
	}
	if a.inFlight {
		a.mu.Unlock()
		return errBusy
	}
	session := a.session
	turnCtx, cancel := context.WithCancel(context.Background())
	a.turnCancel = cancel
	a.inFlight = true
	a.mu.Unlock()

	go func() {
		images, err := loadImages(paths)
		if err != nil {
			a.mu.Lock()
			a.inFlight = false
			a.turnCancel = nil
			a.mu.Unlock()
			cancel()
			a.sink.Emit(EventTurnError, TurnError{Error: err.Error()})
			return
		}
		result, err := session.RunTurnWithImages(turnCtx, text, images)
		a.mu.Lock()
		a.inFlight = false
		if a.turnCancel != nil {
			a.turnCancel = nil
		}
		a.mu.Unlock()
		cancel()
		if err != nil {
			a.sink.Emit(EventTurnError, TurnError{Error: err.Error()})
			return
		}
		a.sink.Emit(EventTurnEnd, turnEndFromResult(result))
		a.refreshTitle(session, text)
	}()
	return nil
}

// loadImages reads each path into a neutral llm.ImageSource, inferring the media
// type from the extension and rejecting oversized files.
func loadImages(paths []string) ([]llm.ImageSource, error) {
	images := make([]llm.ImageSource, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("读取图片失败: %s", filepath.Base(p))
		}
		if info.Size() > maxImageBytes {
			return nil, fmt.Errorf("图片过大(>8MB): %s", filepath.Base(p))
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("读取图片失败: %s", filepath.Base(p))
		}
		images = append(images, llm.ImageSource{MediaType: imageMediaType(p), Data: data})
	}
	return images, nil
}

// imageMediaType maps a file extension to an image media type, defaulting to PNG.
func imageMediaType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "image/png"
	}
}
