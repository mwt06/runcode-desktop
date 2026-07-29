// Package officetool implements the ReadOffice tool: it extracts the *content*
// of an Office document (.docx / .xlsx / .pptx) as compact structured text —
// including fonts, formatting, and layout/position — instead of the raw ZIP
// bytes the generic Read tool would dump.
//
// Office files are OOXML: a ZIP of XML parts. Reading one with the plain Read
// tool returns ~100–200 KB of binary noise per file, which is useless to the
// model and floods the context window (a single such read has been observed to
// cost ~100k tokens and trigger context compaction that drops the user's task).
// ReadOffice returns only the meaningful content, hard-capped so it can never
// re-create that flood.
//
// It is a host tool registered only in the desktop (via engine.Options.ExtraTools),
// like previewtool — the portable engine stays free of Office-parsing deps.
package officetool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/toolpath"
)

// maxResultBytes hard-caps the extracted text. Extracted content is typically an
// order of magnitude smaller than the raw file, but a pathological document must
// still never flood the window — so output is truncated at this ceiling with a
// marker, matching the Read tool's own 200 KB cap in spirit while staying well
// under a single turn's budget.
const maxResultBytes = 120_000

type input struct {
	Path string `json:"path"`
}

// Tool is the ReadOffice tool.
type Tool struct{}

// New returns the ReadOffice tool.
func New() tool.Tool { return Tool{} }

// Name is the tool name the model calls.
func (Tool) Name() string { return "ReadOffice" }

// Description steers the model to this tool for Office files, where plain Read
// returns unreadable binary.
func (Tool) Description() string {
	return "Read an Office document as structured text. Use this INSTEAD of Read for .docx, .xlsx, " +
		"and .pptx files (Read returns their raw binary, which is unusable). " +
		"docx: paragraphs in order with style, font (name/size/bold/italic/color), alignment and indent, plus tables. " +
		"xlsx: each sheet's cell grid with values, plus merged ranges and non-default cell formats. " +
		"pptx: each slide's shapes with their text, position and size (layout). The path is workspace-relative."
}

// InputSchema declares the single workspace-relative "path" argument.
func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"path": {Type: tool.SchemaTypeString, Description: "Workspace-relative path of the .docx/.xlsx/.pptx file to read."},
		},
		Required:             []string{"path"},
		AdditionalProperties: false,
	}
}

// IsConcurrencySafe reports true: extraction only reads a file.
func (Tool) IsConcurrencySafe() bool { return true }

// Run resolves the workspace-relative path, dispatches by extension, and returns
// the extracted structured text.
func (Tool) Run(_ context.Context, raw json.RawMessage, tctx *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse ReadOffice input: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return tool.Result{}, errors.New("path is required")
	}

	abs, err := resolveInWorkspace(in.Path, tctx)
	if err != nil {
		return tool.Result{}, err
	}

	ext := strings.ToLower(filepath.Ext(abs))
	var cb *capBuf
	switch ext {
	case ".docx":
		cb, err = extractDocx(abs)
	case ".xlsx":
		cb, err = extractXlsx(abs)
	case ".pptx":
		cb, err = extractPptx(abs)
	case ".doc", ".xls", ".ppt":
		return tool.Result{}, fmt.Errorf("legacy binary %s is not supported; save it as .docx/.xlsx/.pptx first", ext)
	default:
		return tool.Result{}, fmt.Errorf("unsupported file type %q (ReadOffice handles .docx, .xlsx, .pptx)", ext)
	}
	if err != nil {
		return tool.Result{}, err
	}

	return tool.Result{
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: render(cb)}},
	}, nil
}

// render turns a finished capBuf into the model-facing text: every content line
// gets a 1-based "N\t" prefix (the same addressing convention Read/Edit use, so
// the model can reference an exact line to continue extracting or editing),
// followed by the truncation marker when the ceiling was hit.
func render(cb *capBuf) string {
	content := cb.content()
	if content == "" {
		if cb.truncated() {
			return truncationMarker
		}
		return "(空文档 / 未提取到内容)"
	}
	lines := strings.Split(content, "\n")
	var out strings.Builder
	for i, line := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		fmt.Fprintf(&out, "%d\t%s", i+1, line)
	}
	if cb.truncated() {
		out.WriteByte('\n')
		out.WriteString(truncationMarker)
	}
	return out.String()
}

// resolveInWorkspace resolves a workspace-relative path to an absolute one and
// verifies it exists, is a regular file, and stays inside the workspace — the
// same containment guard previewtool applies, since ReadOffice is authorized as
// side-effect-free management rather than through the read-path resolver.
func resolveInWorkspace(path string, tctx *tool.Context) (string, error) {
	ws, err := toolpath.WorkspaceRoot(tctx)
	if err != nil {
		return "", err
	}
	abs, err := toolpath.Resolve(path, tctx)
	if err != nil {
		return "", err
	}
	within, err := toolpath.IsWithinResolved(ws, abs)
	if err != nil || !within {
		return "", fmt.Errorf("path is outside the workspace: %s", path)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("path is a directory: %s", path)
	}
	return abs, nil
}

// capBuf accumulates output up to a byte ceiling, then stops and marks the
// result truncated so a huge document cannot flood the context window.
type capBuf struct {
	b   strings.Builder
	max int
	cut bool
}

func newCapBuf() *capBuf { return &capBuf{max: maxResultBytes} }

// writeString appends s unless the ceiling is reached; the final partial write is
// trimmed to valid UTF-8 so a multibyte rune is never split.
func (c *capBuf) writeString(s string) {
	if c.cut {
		return
	}
	if c.b.Len()+len(s) > c.max {
		if rem := c.max - c.b.Len(); rem > 0 {
			c.b.WriteString(strings.ToValidUTF8(s[:rem], ""))
		}
		c.cut = true
		return
	}
	c.b.WriteString(s)
}

func (c *capBuf) writef(format string, args ...any) { c.writeString(fmt.Sprintf(format, args...)) }

// truncated reports whether output hit the ceiling.
func (c *capBuf) truncated() bool { return c.cut }

// content returns the accumulated text without line numbers or the truncation
// marker; render adds those.
func (c *capBuf) content() string { return c.b.String() }

const truncationMarker = "…[输出已截断：文档更大，仅显示前一部分。如需精确的完整内容，用脚本按需提取。]"
