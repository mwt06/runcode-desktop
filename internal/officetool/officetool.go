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
	// Offset is the 1-based line to start from, using the line numbers this tool
	// prints. Zero means the beginning.
	Offset int `json:"offset"`
	// Limit caps how many lines come back. Zero means as many as the per-call
	// ceiling allows.
	Limit int `json:"limit"`
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
		"pptx: each slide's shapes with their text, position and size (layout). " +
		"Output is line-numbered and capped per call, so a large document comes back one page at a time: " +
		"the first line of every result states which lines it covers and whether the document ends there. " +
		"When it does not, the result ends with the exact offset to pass back — call again with that offset " +
		"to get the next page, and repeat until the header says 已到末尾. " +
		"Never treat a page as the whole document. The path is workspace-relative."
}

// InputSchema declares the workspace-relative path plus the line window.
func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"path": {Type: tool.SchemaTypeString, Description: "Workspace-relative path of the .docx/.xlsx/.pptx file to read."},
			"offset": {
				Type: tool.SchemaTypeInteger,
				Description: "1-based line to start from, using the line numbers in this tool's own output. " +
					"Omit for the beginning; pass the offset a truncated result reports to continue where it stopped.",
			},
			"limit": {
				Type:        tool.SchemaTypeInteger,
				Description: "Maximum number of lines to return. Omit for as many as fit in one call.",
			},
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
	cb := newCapBuf(in.Offset, in.Limit)
	switch ext {
	case ".docx":
		err = extractDocx(abs, cb)
	case ".xlsx":
		err = extractXlsx(abs, cb)
	case ".pptx":
		err = extractPptx(abs, cb)
	case ".doc", ".xls", ".ppt":
		return tool.Result{}, fmt.Errorf("legacy binary %s is not supported; save it as .docx/.xlsx/.pptx first", ext)
	default:
		return tool.Result{}, fmt.Errorf("unsupported file type %q (ReadOffice handles .docx, .xlsx, .pptx)", ext)
	}
	if err != nil {
		return tool.Result{}, err
	}
	cb.flush()

	return tool.Result{
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: render(cb, filepath.Base(abs))}},
	}, nil
}

// render turns a finished capBuf into the model-facing text: a header naming the
// window, then every kept line with its global "N\t" prefix (the same addressing
// convention Read/Edit use), then — when the document continues past the window —
// the exact offset to pass back. The header always states which lines these are,
// so a partial read can never be mistaken for the whole document.
func render(cb *capBuf, name string) string {
	var out strings.Builder
	out.WriteString(cb.header(name))

	content := cb.content()
	if content == "" {
		return out.String() // the header already explains why nothing came back
	}
	for i, line := range strings.Split(content, "\n") {
		fmt.Fprintf(&out, "\n%d\t%s", cb.first+i, line)
	}
	if cb.more {
		fmt.Fprintf(&out, "\n"+continuationMarker, cb.last+1)
	}
	return out.String()
}

// header states what this result covers. When extraction ran to the end (nothing
// was cut), the line counter is the document's true length, so the total is
// reported; when the window closed early there is no total to report — only the
// fact that more remains, which the continuation marker then makes actionable.
func (c *capBuf) header(name string) string {
	switch {
	case c.line == 0:
		return fmt.Sprintf("[ReadOffice] %s · 空文档 / 未提取到内容", name)
	case c.written == 0:
		return fmt.Sprintf("[ReadOffice] %s · 全文共 %d 行，offset=%d 已超出范围", name, c.line, c.start)
	case c.more:
		return fmt.Sprintf("[ReadOffice] %s · 第 %d–%d 行（后面还有内容，未到末尾）", name, c.first, c.last)
	default:
		return fmt.Sprintf("[ReadOffice] %s · 第 %d–%d 行，全文共 %d 行（已到末尾）", name, c.first, c.last, c.line)
	}
}

// continuationMarker tells the model exactly how to get the next page. The old
// wording sent it off to write an extraction script, which threw away the reason
// this tool exists.
const continuationMarker = "…[未完：文档在此之后仍有内容。用 offset=%d 再次调用 ReadOffice 继续读取。]"

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

// capBuf collects the extracted text as numbered lines, keeping only the window
// the caller asked for. Extraction always runs from the document's start — an
// OOXML part must be parsed in order, there is no seeking to paragraph 900 —
// but lines before the window are counted and dropped instead of buffered, and
// the byte ceiling applies only to what is kept. That is what makes a large
// document readable a page at a time rather than permanently cut off at the
// first ceiling.
//
// Line numbers are global (they count from the document's first line, not from
// the window), so the number a caller sees in the output is the number it passes
// back as offset to continue.
type capBuf struct {
	kept    strings.Builder // lines inside the window, joined by "\n"
	partial strings.Builder // the line currently being written

	max   int // byte ceiling for the kept window
	start int // 1-based first line to keep
	limit int // max lines to keep; 0 = as many as the ceiling allows

	line    int  // last line number the extractor produced (1-based)
	first   int  // line number of the first kept line
	last    int  // line number of the last kept line
	written int  // how many lines were kept
	more    bool // stopped early: content remains past the window
	cut     bool // the byte ceiling — not the line limit — closed the window
	done    bool // window closed; further writes are dropped
}

// newCapBuf returns a buffer for the window starting at line offset (<=0 means
// line 1) holding at most limit lines (<=0 means unlimited).
func newCapBuf(offset, limit int) *capBuf {
	if offset < 1 {
		offset = 1
	}
	if limit < 0 {
		limit = 0
	}
	return &capBuf{max: maxResultBytes, start: offset, limit: limit}
}

// writeString feeds text to the buffer. Input arrives in arbitrary chunks, so
// lines are assembled here: a chunk may carry several lines or half of one.
func (c *capBuf) writeString(s string) {
	if c.done {
		return
	}
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			c.partial.WriteString(s)
			return
		}
		c.partial.WriteString(s[:i])
		c.commitLine()
		s = s[i+1:]
		if c.done {
			return
		}
	}
}

// commitLine closes the line under construction: count it, then keep it, skip
// it, or close the window.
func (c *capBuf) commitLine() {
	text := c.partial.String()
	c.partial.Reset()
	c.line++

	if c.line < c.start {
		return // before the window: counted, not buffered
	}
	if c.limit > 0 && c.written >= c.limit {
		c.more, c.done = true, true
		return
	}

	// +1 accounts for the newline this line will need when joined.
	if c.kept.Len()+len(text)+1 > c.max {
		// A single line longer than the whole ceiling would otherwise return
		// nothing at all, so keep the prefix that fits, trimmed to valid UTF-8 so
		// a multibyte rune is never split.
		if c.written == 0 && len(text) > c.max {
			c.kept.WriteString(strings.ToValidUTF8(text[:c.max], ""))
			c.first, c.last, c.written = c.line, c.line, 1
		}
		c.more, c.cut, c.done = true, true, true
		return
	}

	if c.written == 0 {
		c.first = c.line
	} else {
		c.kept.WriteByte('\n')
	}
	c.kept.WriteString(text)
	c.written++
	c.last = c.line
}

func (c *capBuf) writef(format string, args ...any) { c.writeString(fmt.Sprintf(format, args...)) }

// flush commits a trailing line that was not newline-terminated. Extractors call
// nothing at the end, so Run flushes once extraction returns.
func (c *capBuf) flush() {
	if !c.done && c.partial.Len() > 0 {
		c.commitLine()
	}
}

// full reports that the window is closed, so an extractor can stop early. It is
// the loop guard the extractors use; unlike the old truncated(), stopping here
// never loses content the caller could not have asked for anyway.
func (c *capBuf) full() bool { return c.done }

// truncated reports whether the byte ceiling closed the window.
func (c *capBuf) truncated() bool { return c.cut }

// content returns the kept lines without numbering or the trailing notice.
func (c *capBuf) content() string { return c.kept.String() }
