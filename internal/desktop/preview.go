package desktop

import (
	"errors"
	"os"
	"unicode/utf8"
)

// maxArtifactBytes caps how large a text artifact ReadArtifact returns, so a giant
// file cannot lock up the renderer. Larger files are opened externally instead.
const maxArtifactBytes = 2 << 20 // 2 MiB

// ReadArtifact returns the UTF-8 text of a workspace file for React-rendered
// previews (Markdown/code/text). It rejects paths outside the workspace (both
// lexical ".." and symlink/junction escapes), files over maxArtifactBytes, and
// binary (non-UTF-8) content.
func (a *App) ReadArtifact(relPath string) (string, error) {
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	resolved, err := resolveWithinWorkspace(ws, relPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if info.Size() > maxArtifactBytes {
		return "", errors.New("file too large to preview")
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", errors.New("file is not text")
	}
	return string(data), nil
}

// startPreview (re)starts the workspace preview server. It replaces any running
// one so a workspace switch is clean. Failures are non-fatal (previews of
// text-based types still work via ReadArtifact).
func (a *App) startPreview(workspace string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.preview != nil {
		a.preview.stop()
		a.preview = nil
		a.previewURL = ""
	}
	if workspace == "" {
		return
	}
	ps := newPreviewServer()
	url, err := ps.start(workspace)
	if err != nil {
		return
	}
	a.preview = ps
	a.previewURL = url
}

func (a *App) stopPreview() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.preview != nil {
		a.preview.stop()
		a.preview = nil
		a.previewURL = ""
	}
}

func (a *App) previewBaseURL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.previewURL
}
