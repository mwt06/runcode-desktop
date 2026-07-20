package toolpath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

func WorkspaceRoot(tctx *tool.Context) (string, error) {
	base := ""
	if tctx != nil {
		base = tctx.WorkingDirectory
	}
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		base = cwd
	}
	abs, err := filepath.Abs(filepath.Clean(base))
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	return abs, nil
}

func Resolve(path string, tctx *tool.Context) (string, error) {
	if !filepath.IsAbs(path) {
		base, err := WorkspaceRoot(tctx)
		if err != nil {
			return "", err
		}
		path = filepath.Join(base, path)
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	return abs, nil
}

func IsWithin(workspace string, path string) (bool, error) {
	rel, err := filepath.Rel(workspace, path)
	if err != nil {
		return false, err
	}
	return rel == "." || (rel != "" && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."), nil
}

func IsWithinResolved(workspace string, path string) (bool, error) {
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		resolvedWorkspace = workspace
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolvedPath = path
	}
	return IsWithin(resolvedWorkspace, resolvedPath)
}

type MutationTarget struct {
	Path   string
	Exists bool
	Within bool
}

func ResolveMutationTarget(path string, tctx *tool.Context) (MutationTarget, error) {
	workspace, err := WorkspaceRoot(tctx)
	if err != nil {
		return MutationTarget{}, err
	}
	resolvedPath, err := Resolve(path, tctx)
	if err != nil {
		return MutationTarget{}, err
	}
	_, err = os.Lstat(resolvedPath)
	if err == nil {
		within, err := IsWithinResolved(workspace, resolvedPath)
		if err != nil {
			return MutationTarget{}, fmt.Errorf("check mutation target scope: %w", err)
		}
		return MutationTarget{Path: resolvedPath, Exists: true, Within: within}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return MutationTarget{}, fmt.Errorf("stat mutation target: %w", err)
	}

	// The target does not exist. A missing parent chain is allowed — the writer
	// creates it (mkdir -p) — so walk up to the nearest existing ancestor. That
	// ancestor must be a directory within the workspace, and the target path itself
	// must stay lexically within the workspace (the not-yet-existing components
	// cannot be symlinks). This lets a write create new nested folders instead of
	// failing as an invalid target.
	ancestor := filepath.Dir(resolvedPath)
	for {
		info, statErr := os.Stat(ancestor)
		if statErr == nil {
			if !info.IsDir() {
				return MutationTarget{}, fmt.Errorf("mutation target parent is not a directory")
			}
			break
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return MutationTarget{}, fmt.Errorf("stat mutation target parent: %w", statErr)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return MutationTarget{}, fmt.Errorf("mutation target has no existing ancestor directory")
		}
		ancestor = parent
	}
	within, err := IsWithinResolved(workspace, ancestor)
	if err != nil {
		return MutationTarget{}, fmt.Errorf("check mutation parent scope: %w", err)
	}
	lexWithin, err := IsWithin(workspace, resolvedPath)
	if err != nil {
		return MutationTarget{}, fmt.Errorf("check mutation target scope: %w", err)
	}
	return MutationTarget{Path: resolvedPath, Exists: false, Within: within && lexWithin}, nil
}
