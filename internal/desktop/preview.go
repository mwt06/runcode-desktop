package desktop

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"unicode/utf8"

	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
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
		return "", wireError(err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", wireError(artifactFSError(relPath, err))
	}
	if info.Size() > maxArtifactBytes {
		return "", wireError(errors.New("file too large to preview"))
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", wireError(artifactFSError(relPath, err))
	}
	if !utf8.Valid(data) {
		return "", wireError(errors.New("file is not text"))
	}
	return string(data), nil
}

// artifactFSError turns a filesystem failure on an artifact into something the
// user can act on.
//
// A preview is opened from a card in the conversation, so its path is a claim
// made when the reply was written and may have gone stale since — a later step
// (or the user) moves, renames or deletes the file, and the card still points at
// where it used to be. That is the common case here and deserves to say so.
//
// The raw error must not reach the UI: os.Stat on Windows reports the syscall it
// used, so a missing file surfaced as "GetFileAttributesEx D:\...\x.md: The system
// cannot find the file specified" — an absolute path plus the name of an API the
// user never called, to describe "that file isn't there any more". The path is
// echoed back in the workspace-relative form the card showed, not the resolved
// one, so it matches what the user is looking at.
func artifactFSError(relPath string, err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Coded, not just worded: a card pointing at a file that is gone is the one
		// case the UI wants to handle by *not* opening a tab at all, rather than by
		// showing text. Everything else stays a message, because there is something
		// for the user to react to.
		return &protocol.Error{
			Code:    protocol.ErrCodeNotFound,
			Message: fmt.Sprintf("文件不存在，可能已被移动、重命名或删除：%s", relPath),
		}
	case errors.Is(err, os.ErrPermission):
		return fmt.Errorf("没有读取权限：%s", relPath)
	default:
		// Cause unknown (locked file, I/O error, unreadable mount): name the file in
		// the user's terms and keep the underlying error, which is all we have.
		return fmt.Errorf("读取文件失败：%s（%w）", relPath, err)
	}
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
		return "", wireError(err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", wireError(artifactFSError(relPath, err))
	}
	if info.IsDir() {
		return "", wireError(errors.New("path is a directory"))
	}
	if info.Size() > maxArtifactBinaryBytes {
		return "", wireError(fmt.Errorf("file too large to preview (%d MiB max)", maxArtifactBinaryBytes>>20))
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", wireError(artifactFSError(relPath, err))
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// previewRef 是一个工作区的预览服务器加它的引用数。
//
// 按工作区共享而不是按会话:同一目录开两个会话没有理由起两台服务器(两个端口、
// 两份文件句柄),而且两边看到的 URL 应该一致。refs 归零才真的停。
type previewRef struct {
	srv  *previewServer
	url  string
	refs int
}

// startPreview 为 workspace 取一台预览服务器:已经有就加一次引用,没有才起。
// 失败不致命(文本类预览走 ReadArtifact,不依赖这台服务器)。
func (a *App) startPreview(workspace string) {
	if workspace == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if ref := a.previews[workspace]; ref != nil {
		ref.refs++
		return
	}
	ps := newPreviewServer()
	url, err := ps.start(workspace)
	if err != nil {
		return
	}
	if a.previews == nil {
		a.previews = map[string]*previewRef{}
	}
	a.previews[workspace] = &previewRef{srv: ps, url: url, refs: 1}
}

// stopPreview 释放一次引用;归零时停掉服务器并从表里删掉。
// 传空工作区(会话本来就没开起来)是 no-op。
func (a *App) stopPreview(workspace string) {
	if workspace == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ref := a.previews[workspace]
	if ref == nil {
		return
	}
	ref.refs--
	if ref.refs > 0 {
		return
	}
	ref.srv.stop()
	delete(a.previews, workspace)
}

// previewBaseURL 是**聚焦会话**那个工作区的预览地址;没有会话或没起成服务器时
// 是空串(SessionInfo 里这一栏为空,前端据此退回 ReadArtifact 那条路)。
func (a *App) previewBaseURL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.entryLocked(a.focused)
	if e == nil {
		return ""
	}
	if ref := a.previews[e.workspace]; ref != nil {
		return ref.url
	}
	return ""
}
