package desktop

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"unicode/utf8"
)

// maxArtifactBytes caps how large a text artifact ReadArtifact returns, so a giant
// file cannot lock up the renderer. Larger files are opened externally instead.
const maxArtifactBytes = 2 << 20 // 2 MiB

// maxArtifactBinaryBytes caps ReadArtifactBytes. Office documents (docx/pptx/xlsx)
// are zip archives that the frontend renders in-place, so this is well above the
// text cap but still bounded — a huge file is opened externally instead of streamed
// as base64 (which inflates ~33%) through the Wails bridge and into the renderer.
const maxArtifactBinaryBytes = 25 << 20 // 25 MiB

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

// ReadArtifactBytes returns a workspace file's raw bytes as base64, for previews
// the renderer needs the binary of — Office documents (docx/pptx/xlsx), which are
// zip archives. It shares ReadArtifact's containment (rejecting paths outside the
// workspace, lexically or via symlink/junction) but, unlike it, does not require
// UTF-8 text and allows up to maxArtifactBinaryBytes.
//
// Bytes go through the Wails bridge rather than the loopback preview server on
// purpose: the frontend fetches these with fetch()/arrayBuffer, which is CORS-gated
// against the server's separate origin, and the bridge is also available even when
// the preview server failed to start. (The server still backs iframe/img previews,
// which are not CORS-gated.)
func (a *App) ReadArtifactBytes(relPath string) (string, error) {
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
	if info.IsDir() {
		return "", errors.New("path is a directory")
	}
	if info.Size() > maxArtifactBinaryBytes {
		return "", fmt.Errorf("file too large to preview (%d MiB max)", maxArtifactBinaryBytes>>20)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
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
