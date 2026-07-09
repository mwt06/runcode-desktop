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

// maxStatCells bounds Stat's O(n*m) comparison time (its space is already linear).
// Past it — or for binary input — Stat returns a coarse estimate rather than
// spending seconds comparing a pathological pair.
const maxStatCells = 100_000_000

// Stat returns the exact number of added and removed lines between oldText and
// newText, without the display truncation Unified applies, and in space linear in
// the shorter side. Binary input, or a pair whose comparison would exceed
// maxStatCells, yields a coarse estimate (all added / all removed) instead.
func Stat(oldText, newText string) (added, removed int) {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	// The division (not multiplication) avoids overflow on huge inputs.
	tooBig := len(oldLines) > 0 && len(newLines) > maxStatCells/len(oldLines)
	if looksBinary(oldText) || looksBinary(newText) || tooBig {
		return len(newLines), len(oldLines)
	}
	common := lcsLen(oldLines, newLines)
	return len(newLines) - common, len(oldLines) - common
}

// lcsLen returns the length of the longest common subsequence of a and b using two
// rolling rows: O(len(a)*len(b)) time but only O(min(len(a),len(b))) space, so it
// never allocates the full O(n*m) table Unified's lcsOps builds. Callers bound the
// time via maxStatCells.
func lcsLen(a, b []string) int {
	if len(b) > len(a) {
		a, b = b, a // roll over the shorter dimension
	}
	prev := make([]int32, len(b)+1)
	curr := make([]int32, len(b)+1)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				curr[j] = prev[j+1] + 1
			} else if prev[j] >= curr[j+1] {
				curr[j] = prev[j]
			} else {
				curr[j] = curr[j+1]
			}
		}
		prev, curr = curr, prev
	}
	return int(prev[0])
}
