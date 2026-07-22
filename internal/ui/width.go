package ui

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// The TUI budgets bottom-block height by counting lines, assuming no line ever
// wraps. That only holds if width is measured in terminal cells — CJK characters
// occupy two cells but count as one rune, so rune-based math undercounts Chinese
// paths, branch names, and previews, and the overflow wraps a line and shears the
// whole bottom layout. These are the only width/truncation primitives the package
// should use.
//
// widthCond pins EastAsianWidth off instead of using the package default, which
// auto-detects it from the locale: ambiguous-width characters (…, arrows) render
// as one cell in the terminals we target, and a zh_CN environment silently
// flipping them to two would make layout math disagree across machines (and with
// tests). CJK Han/Kana are category Wide — two cells regardless of this flag.
var widthCond = &runewidth.Condition{}

// displayWidth returns the width of s in terminal cells.
func displayWidth(s string) int {
	return widthCond.StringWidth(s)
}

// truncate shortens s to at most width cells, ending with … when it had to cut
// (for ASCII this matches the historical rune-based behavior exactly).
// width <= 0 disables truncation — callers pass computed widths that can go
// non-positive on tiny terminals, where truncating to nothing helps nobody.
func truncate(s string, width int) string {
	if width <= 0 || displayWidth(s) <= width {
		return s
	}
	budget := width - 1 // reserve one cell for the ellipsis
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := widthCond.RuneWidth(r)
		if used+rw > budget {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	if width <= 1 {
		return b.String() // no room left for the ellipsis itself
	}
	return b.String() + "…"
}
