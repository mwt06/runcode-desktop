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
	"strconv"
	"strings"

	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
)

const (
	defaultLimit      = 100
	maxLimit          = 1000
	maxLineBytes      = 64 * 1024
	binarySampleSize  = 8192
	maxEventFileRefs  = 50
	matchedFilesLabel = "matched files"
	// maxMultilineFileBytes bounds how much of a file is read into memory for a
	// multiline (whole-file) match, so a huge file cannot exhaust memory.
	maxMultilineFileBytes = 5 << 20

	outputModeContent = "content"
	outputModeFiles   = "files_with_matches"
	outputModeCount   = "count"
)

type input struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path,omitempty"`
	Glob            string `json:"glob,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	// OutputMode selects what is returned: content (matching lines, the default),
	// files_with_matches (file paths), or count (per-file match counts).
	OutputMode string `json:"output_mode,omitempty"`
	// LineNumbers toggles the leading line number in content mode. nil defaults to
	// true.
	LineNumbers *bool `json:"line_numbers,omitempty"`
	// After/Before/Context add lines of context around each match in content mode
	// (ripgrep -A/-B/-C). Context sets both sides.
	After   int `json:"after,omitempty"`
	Before  int `json:"before,omitempty"`
	Context int `json:"context,omitempty"`
	// Multiline lets the pattern span lines (. matches newlines); context lines
	// are not applied in this mode.
	Multiline bool `json:"multiline,omitempty"`
}

type Tool struct{}

func New() tool.Tool {
	return Tool{}
}

func (Tool) Name() string {
	return "Grep"
}

func (Tool) Description() string {
	return "Search workspace files for a regular expression. Supports output modes (content/files_with_matches/count), context lines, and multiline matching."
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
			"output_mode": {
				Type:        tool.SchemaTypeString,
				Description: "content (matching lines, default), files_with_matches (file paths), or count (per-file counts).",
				Enum:        []any{outputModeContent, outputModeFiles, outputModeCount},
			},
			"line_numbers": {
				Type:        tool.SchemaTypeBoolean,
				Description: "Show line numbers in content mode (default true).",
			},
			"after": {
				Type:        tool.SchemaTypeInteger,
				Description: "Lines of trailing context after each match (content mode).",
			},
			"before": {
				Type:        tool.SchemaTypeInteger,
				Description: "Lines of leading context before each match (content mode).",
			},
			"context": {
				Type:        tool.SchemaTypeInteger,
				Description: "Lines of context on both sides of each match (content mode).",
			},
			"multiline": {
				Type:        tool.SchemaTypeBoolean,
				Description: "Let the pattern match across lines (. matches newlines).",
				Default:     false,
			},
			"limit": {
				Type:        tool.SchemaTypeInteger,
				Description: "Maximum matching lines (content) or files (files/count) to return.",
				Default:     defaultLimit,
			},
		},
		Required:             []string{"pattern"},
		AdditionalProperties: false,
	}
}

func (Tool) IsConcurrencySafe() bool {
	return true
}

// options is the validated, normalized form of input.
type options struct {
	re          *regexp.Regexp
	mode        string
	limit       int
	lineNumbers bool
	after       int
	before      int
	multiline   bool
	glob        string
}

func (Tool) Run(ctx context.Context, raw json.RawMessage, tctx *tool.Context, out chan<- tool.Event) (tool.Result, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse grep input: %w", err)
	}
	opts, err := normalizeOptions(in)
	if err != nil {
		return tool.Result{}, err
	}

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

	result, err := search(ctx, workspace, searchPath, opts)
	if err != nil {
		return tool.Result{}, err
	}
	emitMatchedFilesEvent(out, result.Files)

	text := strings.Join(result.Output, "\n")
	if result.Truncated {
		if text != "" {
			text += "\n"
		}
		text += "[output truncated]"
	}
	if text == "" {
		text = "No matches found."
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: text}}}, nil
}

func normalizeOptions(in input) (options, error) {
	if strings.TrimSpace(in.Pattern) == "" {
		return options{}, errors.New("pattern is required")
	}
	if in.Glob != "" {
		if err := validateGlob(in.Glob); err != nil {
			return options{}, err
		}
	}
	mode := in.OutputMode
	if mode == "" {
		mode = outputModeContent
	}
	if mode != outputModeContent && mode != outputModeFiles && mode != outputModeCount {
		return options{}, fmt.Errorf("invalid output_mode %q (want content, files_with_matches, or count)", in.OutputMode)
	}

	pattern := in.Pattern
	flags := ""
	if in.CaseInsensitive {
		flags += "i"
	}
	if in.Multiline {
		flags += "s" // dot matches newline
	}
	if flags != "" {
		pattern = "(?" + flags + ")" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return options{}, fmt.Errorf("compile pattern: %w", err)
	}

	after, before := in.After, in.Before
	if in.Context > 0 {
		if in.Context > after {
			after = in.Context
		}
		if in.Context > before {
			before = in.Context
		}
	}
	lineNumbers := true
	if in.LineNumbers != nil {
		lineNumbers = *in.LineNumbers
	}
	return options{
		re:          re,
		mode:        mode,
		limit:       normalizeLimit(in.Limit),
		lineNumbers: lineNumbers,
		after:       clampNonNegative(after),
		before:      clampNonNegative(before),
		multiline:   in.Multiline,
		glob:        in.Glob,
	}, nil
}

type searchResult struct {
	Output    []string
	Files     []string
	Truncated bool
}

func emitMatchedFilesEvent(out chan<- tool.Event, paths []string) {
	if out == nil || len(paths) == 0 {
		return
	}
	refs := make([]tool.FileReference, 0, min(len(paths), maxEventFileRefs))
	for _, path := range paths {
		if len(refs) >= maxEventFileRefs {
			break
		}
		refs = append(refs, tool.FileReference{Path: path, Kind: tool.FileReferenceMatched})
	}
	select {
	case out <- tool.Event{Type: tool.EventTypeProgress, Message: matchedFilesLabel, Files: refs, FilesTotal: len(paths)}:
	default:
	}
}

func search(ctx context.Context, workspace, searchPath string, opts options) (searchResult, error) {
	files, err := collectFiles(ctx, workspace, searchPath, opts.glob)
	if err != nil {
		return searchResult{}, err
	}

	var (
		output     []string
		matchedRel []string
		seen       = map[string]struct{}{}
		matchLines int // content-mode budget
		filesOut   int // files/count-mode budget
		truncated  bool
	)
	for _, filePath := range files {
		if err := ctx.Err(); err != nil {
			return searchResult{}, err
		}
		rel, err := filepath.Rel(workspace, filePath)
		if err != nil {
			continue
		}
		slashRel := filepath.ToSlash(rel)

		content, count, err := scanFile(ctx, filePath, slashRel, opts, opts.limit-matchLines)
		if err != nil || count == 0 {
			continue
		}
		if _, ok := seen[slashRel]; !ok {
			seen[slashRel] = struct{}{}
			matchedRel = append(matchedRel, slashRel)
		}

		switch opts.mode {
		case outputModeFiles:
			if filesOut >= opts.limit {
				truncated = true
			} else {
				output = append(output, slashRel)
				filesOut++
			}
		case outputModeCount:
			if filesOut >= opts.limit {
				truncated = true
			} else {
				output = append(output, slashRel+":"+strconv.Itoa(count))
				filesOut++
			}
		default: // content
			output = append(output, content...)
			matchLines += count
			if matchLines >= opts.limit {
				truncated = true
			}
		}
		if truncated {
			break
		}
	}
	return searchResult{Output: output, Files: matchedRel, Truncated: truncated}, nil
}

// scanFile returns the content lines (content mode only) and the match count for
// one file. remaining bounds how many matching lines content mode may emit.
func scanFile(ctx context.Context, filePath, slashRel string, opts options, remaining int) ([]string, int, error) {
	if remaining <= 0 && opts.mode == outputModeContent {
		return nil, 0, nil
	}
	if isBinaryFile(filePath) {
		return nil, 0, nil
	}
	if opts.multiline {
		return scanMultiline(filePath, slashRel, opts)
	}
	return scanLines(ctx, filePath, slashRel, opts, remaining)
}

// scanLines streams a file line by line, emitting matches (and context in content
// mode) ripgrep-style: "path:line:text" for a match, "path-line-text" for
// context, and "--" between non-adjacent groups.
func scanLines(ctx context.Context, filePath, slashRel string, opts options, remaining int) ([]string, int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	contentMode := opts.mode == outputModeContent

	type bufLine struct {
		no   int
		text string
	}
	var before []bufLine
	var out []string
	count := 0
	afterRemaining := 0
	lastEmitted := 0
	lineNo := 0

	emit := func(no int, text string, isMatch bool) {
		if len(out) > 0 && no > lastEmitted+1 {
			out = append(out, "--")
		}
		out = append(out, formatLine(slashRel, no, text, isMatch, opts.lineNumbers))
		lastEmitted = no
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		line, err := readLineBounded(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		lineNo++
		isMatch := opts.re.MatchString(line)
		if isMatch {
			count++
			if contentMode {
				if count > remaining {
					count-- // do not count an unemitted match
					break
				}
				for _, b := range before {
					if b.no > lastEmitted {
						emit(b.no, b.text, false)
					}
				}
				emit(lineNo, line, true)
				afterRemaining = opts.after
			}
			before = before[:0]
			continue
		}
		if !contentMode {
			continue
		}
		if afterRemaining > 0 {
			emit(lineNo, line, false)
			afterRemaining--
			before = before[:0]
			continue
		}
		if opts.before > 0 {
			before = append(before, bufLine{lineNo, line})
			if len(before) > opts.before {
				before = before[1:]
			}
		}
	}
	return out, count, nil
}

// scanMultiline reads the whole (bounded) file and matches across lines. It
// reports one content line per match (the line where the match starts); context
// lines are not applied in this mode.
func scanMultiline(filePath, slashRel string, opts options) ([]string, int, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, 0, err
	}
	if len(data) > maxMultilineFileBytes {
		data = data[:maxMultilineFileBytes]
	}
	locs := opts.re.FindAllIndex(data, -1)
	if len(locs) == 0 {
		return nil, 0, nil
	}
	if opts.mode != outputModeContent {
		return nil, len(locs), nil
	}
	var out []string
	emittedLine := map[int]struct{}{}
	for _, loc := range locs {
		lineNo := 1 + bytes.Count(data[:loc[0]], []byte{'\n'})
		if _, dup := emittedLine[lineNo]; dup {
			continue
		}
		emittedLine[lineNo] = struct{}{}
		out = append(out, formatLine(slashRel, lineNo, lineTextAt(data, loc[0]), true, opts.lineNumbers))
	}
	return out, len(locs), nil
}

// lineTextAt returns the full text of the line containing byte offset off.
func lineTextAt(data []byte, off int) string {
	start := bytes.LastIndexByte(data[:off], '\n') + 1
	end := bytes.IndexByte(data[off:], '\n')
	if end < 0 {
		return string(data[start:])
	}
	return string(data[start : off+end])
}

// formatLine renders one output line. A match uses ":" separators, context "-",
// matching ripgrep so a reader can tell them apart.
func formatLine(file string, lineNo int, text string, isMatch bool, lineNumbers bool) string {
	sep := "-"
	if isMatch {
		sep = ":"
	}
	if lineNumbers {
		return file + sep + strconv.Itoa(lineNo) + sep + text
	}
	return file + sep + text
}

func collectFiles(ctx context.Context, workspace, searchPath, globPattern string) ([]string, error) {
	info, err := os.Stat(searchPath)
	if err != nil {
		return nil, fmt.Errorf("stat search path: %w", err)
	}
	var files []string
	if !info.IsDir() {
		files = append(files, searchPath)
		return files, nil
	}
	err = filepath.WalkDir(searchPath, func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			// Skip VCS internals and runcode's own bookkeeping (.runcode holds the
			// session logs and permissions file) — neither is user content.
			if name := entry.Name(); (name == ".git" || name == ".runcode") && filePath != searchPath {
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
		return nil, err
	}
	sort.Strings(files)
	return files, nil
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

func clampNonNegative(v int) int {
	if v < 0 {
		return 0
	}
	return v
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
