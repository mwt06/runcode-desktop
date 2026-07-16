package projectctx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wt68/runcode/engine/toolpath"
)

const DefaultMaxBytes = 64 * 1024

var candidateNames = []string{"RUNCODE.md", "CLAUDE.md"}

type LoadOptions struct {
	CWD      string
	MaxBytes int
}

type Result struct {
	Path      string
	Content   string
	Truncated bool
}

func Load(opts LoadOptions) (Result, error) {
	workspace, err := workspaceRoot(opts.CWD)
	if err != nil {
		return Result{}, err
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}

	for dir := workspace; ; dir = filepath.Dir(dir) {
		for _, name := range candidateNames {
			candidate := filepath.Join(dir, name)
			result, ok, err := loadCandidate(dir, candidate, maxBytes)
			if err != nil || ok {
				return result, err
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Result{}, nil
		}
	}
}

func Format(result Result) string {
	if strings.TrimSpace(result.Content) == "" {
		return ""
	}
	text := fmt.Sprintf("Project context from %s:\n\n%s", filepath.Base(result.Path), result.Content)
	if result.Truncated {
		text += "\n[project context truncated]"
	}
	return text
}

func workspaceRoot(cwd string) (string, error) {
	if cwd == "" {
		current, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		cwd = current
	}
	abs, err := filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	return abs, nil
}

func loadCandidate(searchDir string, path string, maxBytes int) (Result, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{}, false, nil
		}
		return Result{}, false, fmt.Errorf("stat project context: %w", err)
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return Result{}, false, nil
	}
	within, err := toolpath.IsWithinResolved(searchDir, path)
	if err != nil {
		return Result{}, false, fmt.Errorf("check project context scope: %w", err)
	}
	if !within {
		return Result{}, false, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return Result{}, false, fmt.Errorf("open project context: %w", err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return Result{}, false, fmt.Errorf("read project context: %w", err)
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	content := string(data)
	if strings.TrimSpace(content) == "" {
		return Result{}, true, nil
	}
	return Result{Path: path, Content: content, Truncated: truncated}, true, nil
}
