// Package diff produces bounded, display-only unified line diffs for the TUI.
// Output is rendered as tool.OutputLine values and is never sent to the model
// or recorded to telemetry/transcripts.
package diff

import (
	"fmt"
	"strings"

	"github.com/wt68/runcode/pkg/tool"
)

// Options bounds diff computation and rendering.
type Options struct {
	// Context is the number of unchanged lines shown around each change.
	Context int
	// MaxLines caps the number of emitted output lines.
	MaxLines int
	// MaxInput is the largest line count (either side) the diff will process
	// before falling back to a summary line.
	MaxInput int
}

// DefaultOptions returns sensible bounds for tool diffs.
func DefaultOptions() Options {
	return Options{Context: 3, MaxLines: 200, MaxInput: 2000}
}

func (o Options) withDefaults() Options {
	defaults := DefaultOptions()
	if o.Context <= 0 {
		o.Context = defaults.Context
	}
	if o.MaxLines <= 0 {
		o.MaxLines = defaults.MaxLines
	}
	if o.MaxInput <= 0 {
		o.MaxInput = defaults.MaxInput
	}
	return o
}

type opKind int

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

type op struct {
	kind opKind
	text string
}

// Unified returns a bounded unified line diff of oldText vs newText as display
// output lines. Binary or oversized input yields a single info line instead.
func Unified(oldText string, newText string, opts Options) []tool.OutputLine {
	opts = opts.withDefaults()
	if looksBinary(oldText) || looksBinary(newText) {
		return []tool.OutputLine{{Stream: tool.OutputStreamInfo, Text: "binary content, diff omitted"}}
	}
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	if len(oldLines) > opts.MaxInput || len(newLines) > opts.MaxInput {
		return []tool.OutputLine{{Stream: tool.OutputStreamInfo, Text: fmt.Sprintf("large file (%d→%d lines), diff omitted", len(oldLines), len(newLines))}}
	}
	ops := lcsOps(oldLines, newLines)
	changed := false
	for _, o := range ops {
		if o.kind != opEqual {
			changed = true
			break
		}
	}
	if !changed {
		return []tool.OutputLine{{Stream: tool.OutputStreamInfo, Text: "no changes"}}
	}
	return renderUnified(ops, opts.Context, opts.MaxLines)
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	// Drop the single trailing empty element produced by a trailing newline.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

func looksBinary(text string) bool {
	limit := len(text)
	if limit > 8192 {
		limit = 8192
	}
	return strings.IndexByte(text[:limit], 0) >= 0
}

// lcsOps aligns a and b via a longest-common-subsequence table and returns the
// edit script. It is O(n*m) in time and memory, which is acceptable because
// callers gate on Options.MaxInput.
func lcsOps(a []string, b []string) []op {
	n, m := len(a), len(b)
	width := m + 1
	dp := make([]int32, (n+1)*width)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i*width+j] = dp[(i+1)*width+(j+1)] + 1
			} else if dp[(i+1)*width+j] >= dp[i*width+(j+1)] {
				dp[i*width+j] = dp[(i+1)*width+j]
			} else {
				dp[i*width+j] = dp[i*width+(j+1)]
			}
		}
	}
	ops := make([]op, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{kind: opEqual, text: a[i]})
			i++
			j++
		case dp[(i+1)*width+j] >= dp[i*width+(j+1)]:
			ops = append(ops, op{kind: opDelete, text: a[i]})
			i++
		default:
			ops = append(ops, op{kind: opInsert, text: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{kind: opDelete, text: a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{kind: opInsert, text: b[j]})
	}
	return ops
}

func renderUnified(ops []op, context int, maxLines int) []tool.OutputLine {
	n := len(ops)
	keep := make([]bool, n)
	for idx, o := range ops {
		if o.kind == opEqual {
			continue
		}
		lo := idx - context
		if lo < 0 {
			lo = 0
		}
		hi := idx + context
		if hi >= n {
			hi = n - 1
		}
		for k := lo; k <= hi; k++ {
			keep[k] = true
		}
	}

	lines := make([]tool.OutputLine, 0, maxLines)
	gap := false
	for idx := 0; idx < n; idx++ {
		if !keep[idx] {
			gap = true
			continue
		}
		if gap && len(lines) > 0 {
			lines = append(lines, tool.OutputLine{Stream: tool.OutputStreamInfo, Text: "⋯"})
		}
		gap = false
		lines = append(lines, diffLine(ops[idx]))
		if len(lines) >= maxLines {
			if remaining := countKept(keep, idx+1); remaining > 0 {
				lines = append(lines, tool.OutputLine{Stream: tool.OutputStreamInfo, Text: fmt.Sprintf("… +%d more lines", remaining)})
			}
			break
		}
	}
	return lines
}

func diffLine(o op) tool.OutputLine {
	switch o.kind {
	case opDelete:
		return tool.OutputLine{Stream: tool.OutputStreamDiffDel, Text: "- " + o.text}
	case opInsert:
		return tool.OutputLine{Stream: tool.OutputStreamDiffAdd, Text: "+ " + o.text}
	default:
		return tool.OutputLine{Stream: tool.OutputStreamDiffContext, Text: "  " + o.text}
	}
}

func countKept(keep []bool, from int) int {
	count := 0
	for k := from; k < len(keep); k++ {
		if keep[k] {
			count++
		}
	}
	return count
}

// statMaxInput bounds the O(n*m) LCS for Stat; past it Stat returns a coarse
// estimate (both sides treated as fully changed) instead of blowing up.
const statMaxInput = 50000

// Stat returns the exact number of added and removed lines between oldText and
// newText, without the display truncation Unified applies. Binary or oversized
// input yields a coarse estimate rather than an exact count.
func Stat(oldText, newText string) (added, removed int) {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	if looksBinary(oldText) || looksBinary(newText) {
		return len(newLines), len(oldLines)
	}
	if len(oldLines) > statMaxInput || len(newLines) > statMaxInput {
		return len(newLines), len(oldLines)
	}
	for _, o := range lcsOps(oldLines, newLines) {
		switch o.kind {
		case opInsert:
			added++
		case opDelete:
			removed++
		}
	}
	return added, removed
}
