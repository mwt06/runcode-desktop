package read

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
)

// maxImageBytes bounds an image read so a huge file cannot blow up the request.
const maxImageBytes = 8 << 20

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
	return "Read a file. Text files return line-numbered content; image files (png, jpg, jpeg, gif, webp) return the image itself so you can view it directly."
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
	// Read is read-only: it touches disk and records the file it read into
	// tctx.ReadSet, but the concurrent executor hands each sibling call its own
	// cloned Context (ReadSet included) and merges them back under a lock, so no two
	// Read calls ever share a map. Reads are also always auto-allowed or denied by
	// policy (never a prompt), so a batch of them can't raise interleaved approvals.
	return true
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

	// Image files are returned as an image content block so the model can view them
	// (rather than dumping raw bytes as text).
	if mediaType, ok := imageMediaType(path); ok {
		return readImage(path, info, tctx, mediaType)
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

// imageMediaType maps an image file extension to its media type. ok is false for
// non-image files.
func imageMediaType(path string) (string, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	default:
		return "", false
	}
}

// readImage returns the file as an image content block (plus a short text label),
// recording the read like a text read so read-before-write tracking stays correct.
func readImage(path string, info os.FileInfo, tctx *tool.Context, mediaType string) (tool.Result, error) {
	if info.Size() > maxImageBytes {
		return tool.Result{}, fmt.Errorf("image is too large (%d bytes; limit %d)", info.Size(), maxImageBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return tool.Result{}, fmt.Errorf("read image: %w", err)
	}
	if tctx != nil {
		if tctx.ReadSet == nil {
			tctx.ReadSet = make(map[string]tool.ReadFile)
		}
		tctx.ReadSet[path] = tool.ReadFile{Path: path, Size: info.Size(), ModTime: info.ModTime(), Complete: true}
	}
	return tool.Result{
		Content: []tool.ResultContent{
			{Type: tool.ResultContentTypeText, Text: fmt.Sprintf("[image: %s]", filepath.Base(path))},
			{Type: tool.ResultContentTypeImage, Image: &tool.ResultImage{MediaType: mediaType, Data: data}},
		},
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
