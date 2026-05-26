package grep

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
)

const (
	defaultLimit     = 100
	maxLimit         = 1000
	maxLineBytes     = 64 * 1024
	binarySampleSize = 8192
)

type input struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path,omitempty"`
	Glob            string `json:"glob,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
	Limit           int    `json:"limit,omitempty"`
}

type Tool struct{}

func New() tool.Tool {
	return Tool{}
}

func (Tool) Name() string {
	return "Grep"
}

func (Tool) Description() string {
	return "Search workspace files for lines matching a regular expression."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"pattern": {
				Type:        tool.SchemaTypeString,
				Description: "Go regular expression to search for.",
			},
			"path": {
				Type:        tool.SchemaTypeString,
				Description: "Workspace-relative file or directory to search. Defaults to the workspace root.",
			},
			"glob": {
				Type:        tool.SchemaTypeString,
				Description: "Optional slash-separated glob filter for files, supporting ** for recursive segments.",
			},
			"case_insensitive": {
				Type:        tool.SchemaTypeBoolean,
				Description: "Whether matching should ignore case.",
				Default:     false,
			},
			"limit": {
				Type:        tool.SchemaTypeInteger,
				Description: "Maximum number of matching lines to return.",
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
		return tool.Result{}, fmt.Errorf("parse grep input: %w", err)
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return tool.Result{}, errors.New("pattern is required")
	}
	if in.Glob != "" {
		if err := validateGlob(in.Glob); err != nil {
			return tool.Result{}, err
		}
	}
	pattern := in.Pattern
	if in.CaseInsensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return tool.Result{}, fmt.Errorf("compile pattern: %w", err)
	}
	limit := normalizeLimit(in.Limit)

	workspace, err := toolpath.WorkspaceRoot(tctx)
	if err != nil {
		return tool.Result{}, err
	}
	searchPath := workspace
	if in.Path != "" {
		searchPath, err = toolpath.Resolve(in.Path, tctx)
		if err != nil {
			return tool.Result{}, err
		}
	}
	within, err := toolpath.IsWithinResolved(workspace, searchPath)
	if err != nil {
		return tool.Result{}, fmt.Errorf("check search path scope: %w", err)
	}
	if !within {
		return tool.Result{}, errors.New("path is outside workspace")
	}

	matches, truncated, err := search(ctx, workspace, searchPath, in.Glob, re, limit)
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

func search(ctx context.Context, workspace string, searchPath string, globPattern string, re *regexp.Regexp, limit int) ([]string, bool, error) {
	info, err := os.Stat(searchPath)
	if err != nil {
		return nil, false, fmt.Errorf("stat search path: %w", err)
	}
	var files []string
	if !info.IsDir() {
		files = append(files, searchPath)
	} else {
		err = filepath.WalkDir(searchPath, func(filePath string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == ".git" && filePath != searchPath {
					return filepath.SkipDir
				}
				return nil
			}
			within, err := toolpath.IsWithinResolved(workspace, filePath)
			if err != nil || !within {
				return nil
			}
			if matchesFileGlob(workspace, filePath, globPattern) {
				files = append(files, filePath)
			}
			return nil
		})
		if err != nil {
			return nil, false, err
		}
	}
	sort.Strings(files)

	var matches []string
	truncated := false
	for _, filePath := range files {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		fileMatches, err := grepFile(ctx, workspace, filePath, re, limit-len(matches))
		if err != nil {
			continue
		}
		matches = append(matches, fileMatches...)
		if len(matches) >= limit {
			truncated = true
			break
		}
	}
	return matches, truncated, nil
}

func grepFile(ctx context.Context, workspace string, filePath string, re *regexp.Regexp, remaining int) ([]string, error) {
	if remaining <= 0 {
		return nil, nil
	}
	if isBinaryFile(filePath) {
		return nil, nil
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	rel, err := filepath.Rel(workspace, filePath)
	if err != nil {
		return nil, err
	}
	slashRel := filepath.ToSlash(rel)
	reader := bufio.NewReader(file)
	lineNumber := 0
	var matches []string
	for len(matches) < remaining {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, err := readLineBounded(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		lineNumber++
		if re.MatchString(line) {
			matches = append(matches, fmt.Sprintf("%s:%d:%s", slashRel, lineNumber, line))
		}
	}
	return matches, nil
}

func readLineBounded(reader *bufio.Reader) (string, error) {
	line, isPrefix, err := reader.ReadLine()
	if err != nil {
		return "", err
	}
	var parts [][]byte
	parts = append(parts, line)
	total := len(line)
	for isPrefix {
		fragment, prefix, err := reader.ReadLine()
		if err != nil {
			return "", err
		}
		isPrefix = prefix
		if total < maxLineBytes {
			remaining := maxLineBytes - total
			if len(fragment) > remaining {
				fragment = fragment[:remaining]
			}
			parts = append(parts, fragment)
		}
		total += len(fragment)
	}
	combined := bytes.Join(parts, nil)
	if len(combined) > 0 && combined[len(combined)-1] == '\r' {
		combined = combined[:len(combined)-1]
	}
	return string(combined), nil
}

func isBinaryFile(filePath string) bool {
	file, err := os.Open(filePath)
	if err != nil {
		return true
	}
	defer file.Close()
	buffer := make([]byte, binarySampleSize)
	n, err := file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return true
	}
	return bytes.IndexByte(buffer[:n], 0) >= 0
}

func matchesFileGlob(workspace string, filePath string, globPattern string) bool {
	if globPattern == "" {
		return true
	}
	rel, err := filepath.Rel(workspace, filePath)
	if err != nil {
		return false
	}
	return matchSlashPattern(globPattern, filepath.ToSlash(rel))
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

func validateGlob(pattern string) error {
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
