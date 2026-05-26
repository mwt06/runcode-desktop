package read

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
)

const (
	defaultLimit   = 2000
	maxResultBytes = 200_000
)

type input struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// Tool reads text files and returns line-numbered content.
type Tool struct{}

func New() tool.Tool {
	return Tool{}
}

func (Tool) Name() string {
	return "Read"
}

func (Tool) Description() string {
	return "Read a text file and return line-numbered content."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"path": {
				Type:        tool.SchemaTypeString,
				Description: "Path to the file to read.",
			},
			"offset": {
				Type:        tool.SchemaTypeInteger,
				Description: "Zero-based line offset to start reading from.",
				Default:     0,
			},
			"limit": {
				Type:        tool.SchemaTypeInteger,
				Description: "Maximum number of lines to read.",
				Default:     defaultLimit,
			},
		},
		Required:             []string{"path"},
		AdditionalProperties: false,
	}
}

func (Tool) IsConcurrencySafe() bool {
	return false
}

func (Tool) Run(ctx context.Context, raw json.RawMessage, tctx *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse read input: %w", err)
	}
	if in.Path == "" {
		return tool.Result{}, errors.New("path is required")
	}
	if in.Offset < 0 {
		return tool.Result{}, errors.New("offset must be greater than or equal to 0")
	}
	if in.Limit <= 0 {
		in.Limit = defaultLimit
	}

	path, err := toolpath.Resolve(in.Path, tctx)
	if err != nil {
		return tool.Result{}, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return tool.Result{}, fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return tool.Result{}, fmt.Errorf("path is a directory: %s", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return tool.Result{}, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	readResult, err := readLines(ctx, file, in.Offset, in.Limit)
	if err != nil {
		return tool.Result{}, err
	}

	if tctx != nil {
		if tctx.ReadSet == nil {
			tctx.ReadSet = make(map[string]tool.ReadFile)
		}
		tctx.ReadSet[path] = tool.ReadFile{Path: path, Size: info.Size(), ModTime: info.ModTime(), Complete: readResult.Complete}
	}

	return tool.Result{
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: readResult.Text}},
	}, nil
}

type lineReadResult struct {
	Text     string
	Complete bool
}

func readLines(ctx context.Context, r io.Reader, offset int, limit int) (lineReadResult, error) {
	reader := bufio.NewReader(r)
	var builder strings.Builder
	lineNumber := 0
	written := 0
	truncated := false
	reachedEOF := false

readLoop:
	for written < limit {
		if err := ctx.Err(); err != nil {
			return lineReadResult{}, err
		}

		fragment, isPrefix, err := reader.ReadLine()
		if errors.Is(err, io.EOF) {
			reachedEOF = true
			break
		}
		if err != nil {
			return lineReadResult{}, fmt.Errorf("read file: %w", err)
		}

		lineNumber++
		include := lineNumber > offset
		if include {
			if written > 0 {
				builder.WriteByte('\n')
			}
			if !writeBounded(&builder, []byte(fmt.Sprintf("%d\t", lineNumber))) {
				truncated = true
				break
			}
		}

		for {
			if include {
				part := fragment
				if !isPrefix && len(part) > 0 && part[len(part)-1] == '\r' {
					part = part[:len(part)-1]
				}
				if !writeBounded(&builder, part) {
					truncated = true
					break readLoop
				}
			}
			if !isPrefix {
				break
			}
			if err := ctx.Err(); err != nil {
				return lineReadResult{}, err
			}
			fragment, isPrefix, err = reader.ReadLine()
			if errors.Is(err, io.EOF) {
				reachedEOF = true
				break
			}
			if err != nil {
				return lineReadResult{}, fmt.Errorf("read file: %w", err)
			}
		}

		if include {
			written++
		}
	}
	if truncated {
		builder.WriteString("\n[output truncated]")
	}

	return lineReadResult{Text: builder.String(), Complete: offset == 0 && reachedEOF && !truncated}, nil
}

func writeBounded(builder *strings.Builder, text []byte) bool {
	remaining := maxResultBytes - builder.Len()
	if remaining <= 0 {
		return false
	}
	if len(text) > remaining {
		builder.Write(text[:remaining])
		return false
	}
	builder.Write(text)
	return true
}
