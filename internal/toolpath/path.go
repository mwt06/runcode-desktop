package toolpath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wt68/runcode/pkg/tool"
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

	parent := filepath.Dir(resolvedPath)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return MutationTarget{}, fmt.Errorf("mutation target parent does not exist: %w", err)
		}
		return MutationTarget{}, fmt.Errorf("stat mutation target parent: %w", err)
	}
	if !parentInfo.IsDir() {
		return MutationTarget{}, fmt.Errorf("mutation target parent is not a directory")
	}
	within, err := IsWithinResolved(workspace, parent)
	if err != nil {
		return MutationTarget{}, fmt.Errorf("check mutation parent scope: %w", err)
	}
	return MutationTarget{Path: resolvedPath, Exists: false, Within: within}, nil
}
