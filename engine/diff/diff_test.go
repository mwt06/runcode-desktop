package diff

import (
	"fmt"
	"strings"
	"testing"

	"github.com/wt68/runcode/engine/tool"
)

func streams(lines []tool.OutputLine) []tool.OutputStream {
	out := make([]tool.OutputStream, len(lines))
	for i, line := range lines {
		out[i] = line.Stream
	}
	return out
}

func countStream(lines []tool.OutputLine, stream tool.OutputStream) int {
	count := 0
	for _, line := range lines {
		if line.Stream == stream {
			count++
		}
	}
	return count
}

func TestUnifiedNoChanges(t *testing.T) {
	t.Parallel()
	lines := Unified("a\nb\nc\n", "a\nb\nc\n", DefaultOptions())
	if len(lines) != 1 || lines[0].Stream != tool.OutputStreamInfo || lines[0].Text != "no changes" {
		t.Fatalf("lines = %#v, want single 'no changes' info", lines)
	}
}

func TestUnifiedAllAdded(t *testing.T) {
	t.Parallel()
	lines := Unified("", "x\ny\nz\n", DefaultOptions())
	if got := countStream(lines, tool.OutputStreamDiffAdd); got != 3 {
		t.Fatalf("added = %d, want 3 (%#v)", got, lines)
	}
	if countStream(lines, tool.OutputStreamDiffDel) != 0 {
		t.Fatalf("unexpected deletions: %#v", lines)
	}
	if !strings.HasPrefix(lines[0].Text, "+ ") {
		t.Fatalf("first add line = %q, want '+ ' prefix", lines[0].Text)
	}
}

func TestUnifiedAllRemoved(t *testing.T) {
	t.Parallel()
	lines := Unified("x\ny\n", "", DefaultOptions())
	if got := countStream(lines, tool.OutputStreamDiffDel); got != 2 {
		t.Fatalf("deleted = %d, want 2 (%#v)", got, lines)
	}
	if countStream(lines, tool.OutputStreamDiffAdd) != 0 {
		t.Fatalf("unexpected additions: %#v", lines)
	}
}

func TestUnifiedMixedHunkWithContext(t *testing.T) {
	t.Parallel()
	lines := Unified("a\nb\nc\n", "a\nx\nc\n", DefaultOptions())
	// expect: context a, del b, add x, context c
	if countStream(lines, tool.OutputStreamDiffDel) != 1 || countStream(lines, tool.OutputStreamDiffAdd) != 1 {
		t.Fatalf("want one del + one add, got %#v", streams(lines))
	}
	if countStream(lines, tool.OutputStreamDiffContext) != 2 {
		t.Fatalf("want two context lines, got %#v", streams(lines))
	}
}

func TestUnifiedElidesDistantContext(t *testing.T) {
	t.Parallel()
	var oldB, newB strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&oldB, "line%d\n", i)
		if i == 0 {
			newB.WriteString("CHANGED0\n")
		} else if i == 39 {
			newB.WriteString("CHANGED39\n")
		} else {
			fmt.Fprintf(&newB, "line%d\n", i)
		}
	}
	lines := Unified(oldB.String(), newB.String(), DefaultOptions())
	// The large unchanged middle should be elided with a gap marker.
	gaps := 0
	for _, line := range lines {
		if line.Stream == tool.OutputStreamInfo && line.Text == "⋯" {
			gaps++
		}
	}
	if gaps == 0 {
		t.Fatalf("expected an elision gap between distant changes, got %#v", lines)
	}
	// The untouched middle lines must not all be present.
	if countStream(lines, tool.OutputStreamDiffContext) >= 38 {
		t.Fatalf("context not elided: %d context lines", countStream(lines, tool.OutputStreamDiffContext))
	}
}

func TestUnifiedBinaryFallback(t *testing.T) {
	t.Parallel()
	lines := Unified("ok\n", "bad\x00data", DefaultOptions())
	if len(lines) != 1 || lines[0].Stream != tool.OutputStreamInfo || !strings.Contains(lines[0].Text, "binary") {
		t.Fatalf("lines = %#v, want binary info", lines)
	}
}

func TestUnifiedLargeInputFallback(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x\n", 50)
	lines := Unified("a\n", big, Options{MaxInput: 10})
	if len(lines) != 1 || lines[0].Stream != tool.OutputStreamInfo || !strings.Contains(lines[0].Text, "large file") {
		t.Fatalf("lines = %#v, want large-file info", lines)
	}
}

func TestUnifiedCapsEmittedLines(t *testing.T) {
	t.Parallel()
	var newB strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&newB, "line%d\n", i)
	}
	lines := Unified("", newB.String(), Options{MaxLines: 10})
	if len(lines) > 11 { // 10 emitted + 1 "+N more" info
		t.Fatalf("emitted %d lines, want capped near 10", len(lines))
	}
	last := lines[len(lines)-1]
	if last.Stream != tool.OutputStreamInfo || !strings.Contains(last.Text, "more lines") {
		t.Fatalf("last line = %#v, want truncation info", last)
	}
}
