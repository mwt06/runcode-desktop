package glob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
)

const (
	defaultLimit = 200
	maxLimit     = 1000
)

type input struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type Tool struct{}

func New() tool.Tool {
	return Tool{}
}

func (Tool) Name() string {
	return "Glob"
}

func (Tool) Description() string {
	return "Find workspace files matching a slash-separated glob pattern."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"pattern": {
				Type:        tool.SchemaTypeString,
				Description: "Slash-separated glob pattern to match, supporting ** for recursive segments.",
			},
			"path": {
				Type:        tool.SchemaTypeString,
				Description: "Workspace-relative directory to search. Defaults to the workspace root.",
			},
			"limit": {
				Type:        tool.SchemaTypeInteger,
				Description: "Maximum number of matched files to return.",
				Default:     defaultLimit,
			},
		},
		Required:             []string{"pattern"},
		AdditionalProperties: false,
	}
}

func (Tool) IsConcurrencySafe() bool {
	return false
}

func (Tool) Run(ctx context.Context, raw json.RawMessage, tctx *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse glob input: %w", err)
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return tool.Result{}, errors.New("pattern is required")
	}
	if err := validatePattern(in.Pattern); err != nil {
		return tool.Result{}, err
	}
	limit := normalizeLimit(in.Limit)

	workspace, err := toolpath.WorkspaceRoot(tctx)
	if err != nil {
		return tool.Result{}, err
	}
	searchRoot := workspace
	if in.Path != "" {
		searchRoot, err = toolpath.Resolve(in.Path, tctx)
		if err != nil {
			return tool.Result{}, err
		}
	}
	within, err := toolpath.IsWithinResolved(workspace, searchRoot)
	if err != nil {
		return tool.Result{}, fmt.Errorf("check search path scope: %w", err)
	}
	if !within {
		return tool.Result{}, errors.New("path is outside workspace")
	}

	matches, truncated, err := findMatches(ctx, workspace, searchRoot, in.Pattern, limit)
	if err != nil {
		return tool.Result{}, err
	}
	text := strings.Join(matches, "\n")
	if truncated {
		if text != "" {
			text += "\n"
		}
		text += "[output truncated]"
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: text}}}, nil
}

func findMatches(ctx context.Context, workspace string, searchRoot string, pattern string, limit int) ([]string, bool, error) {
	var matches []string
	truncated := false
	err := filepath.WalkDir(searchRoot, func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" && filePath != searchRoot {
				return filepath.SkipDir
			}
			return nil
		}
		within, err := toolpath.IsWithinResolved(workspace, filePath)
		if err != nil || !within {
			return nil
		}
		rel, err := filepath.Rel(workspace, filePath)
		if err != nil {
			return err
		}
		slashRel := filepath.ToSlash(rel)
		if matchSlashPattern(pattern, slashRel) {
			matches = append(matches, slashRel)
			if len(matches) > limit {
				truncated = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if truncated && len(matches) > limit {
		matches = matches[:limit]
	}
	sort.Strings(matches)
	return matches, truncated, nil
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

func validatePattern(pattern string) error {
	for _, segment := range splitSlashPath(pattern) {
		if segment == "**" {
			continue
		}
		if _, err := path.Match(segment, ""); err != nil {
			return fmt.Errorf("invalid glob pattern: %w", err)
		}
	}
	return nil
}

func matchSlashPattern(pattern string, name string) bool {
	return matchSegments(splitSlashPath(pattern), splitSlashPath(name))
}

func matchSegments(patterns []string, names []string) bool {
	if len(patterns) == 0 {
		return len(names) == 0
	}
	if patterns[0] == "**" {
		for i := 0; i <= len(names); i++ {
			if matchSegments(patterns[1:], names[i:]) {
				return true
			}
		}
		return false
	}
	if len(names) == 0 {
		return false
	}
	matched, err := path.Match(patterns[0], names[0])
	if err != nil || !matched {
		return false
	}
	return matchSegments(patterns[1:], names[1:])
}

func splitSlashPath(value string) []string {
	value = strings.Trim(filepath.ToSlash(value), "/")
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}
