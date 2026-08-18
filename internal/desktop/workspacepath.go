package desktop

import (
	"errors"
	"path/filepath"
	"strings"
)

// resolveWithinWorkspace resolves relPath against workspace ws and returns the
// real (symlink-resolved) absolute path, or an error if ws is empty, relPath
// escapes lexically, or it resolves (via symlink/junction) outside ws. It fails
// closed: a non-existent path, or a reparse point Go cannot walk (Windows
// junctions are ModeIrregular, not ModeSymlink, and abort EvalSymlinks), returns
// an error rather than a path the OS might follow outside ws. This is the single
// containment check reused by ReadArtifact and the preview static server (and, in
// a later task, the open/reveal bindings).
func resolveWithinWorkspace(ws, relPath string) (string, error) {
	if ws == "" {
		return "", errors.New("no active workspace")
	}
	full := filepath.Join(ws, filepath.FromSlash(relPath))
	// Lexical bound first (cheap; catches ".." before touching the filesystem).
	if rel, err := filepath.Rel(ws, full); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path is outside the workspace")
	}
	wsResolved, err := filepath.EvalSymlinks(ws)
	if err != nil {
		wsResolved = ws
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		// 归一化必须发生在这里，不能留给调用方。文件不存在时 EvalSymlinks **先于**
		// 调用方的 os.Stat 失败，报的是带 syscall 名与绝对路径的原始 PathError
		// （"GetFileAttributesEx D:\…\x.md: The system cannot find the file
		// specified"）。原样上抛的话，调用方为"文件没了"精心准备的 artifactFSError
		// 永远被这里截胡——最常见的那个场景反而拿不到该有的提示与 not_found 码。
		return "", artifactFSError(relPath, err)
	}
	if r, err := filepath.Rel(wsResolved, resolved); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", errors.New("path resolves outside the workspace")
	}
	return resolved, nil
}
