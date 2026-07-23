package ui

// 助手回复的 Markdown 渲染与记忆化。

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

// Every viewport refresh re-renders the whole transcript, and a streaming turn
// refreshes on each delta and tool event — without memoization that re-parses
// every earlier (unchanged) message per keystroke of model output, with cost
// growing linearly in conversation length. The memo caches rendered markdown for
// completed messages, and the glamour renderer (expensive to construct) is reused
// until the wrap width changes. The map is cleared wholesale past its cap: a
// rebuild costs one render per visible message, and an eviction policy isn't worth
// the bookkeeping. The mutex serializes bubbletea's render goroutine with tests.
const markdownMemoCap = 512

type markdownMemoKey struct {
	width int
	text  string
}

var (
	markdownMu            sync.Mutex
	markdownMemo          = map[markdownMemoKey]string{}
	markdownRenderer      *glamour.TermRenderer
	markdownRendererWidth int
)

func renderMarkdown(text string, width int, cacheable bool) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	if width < 1 {
		return text
	}
	markdownMu.Lock()
	defer markdownMu.Unlock()
	key := markdownMemoKey{width: width, text: text}
	if cacheable {
		if out, ok := markdownMemo[key]; ok {
			return out
		}
	}
	if markdownRenderer == nil || markdownRendererWidth != width {
		renderer, err := glamour.NewTermRenderer(
			glamour.WithStyles(tuiMarkdownStyle()),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return text
		}
		markdownRenderer, markdownRendererWidth = renderer, width
	}
	rendered, err := markdownRenderer.Render(text)
	if err != nil {
		return text
	}
	out := strings.TrimRight(rendered, "\n")
	if cacheable {
		if len(markdownMemo) >= markdownMemoCap {
			markdownMemo = map[markdownMemoKey]string{}
		}
		markdownMemo[key] = out
	}
	return out
}

// tuiMarkdownStyle 收紧 glamour 的默认深色主题:去掉外边距(TUI 自己管缩进),
// 标题改用行首标记而非底色块。
func tuiMarkdownStyle() ansi.StyleConfig {
	style := styles.DarkStyleConfig
	zero := uint(0)
	style.Document.Margin = &zero
	style.CodeBlock.Margin = &zero
	style.H1.Prefix = "▌ "
	style.H1.Suffix = ""
	style.H1.BackgroundColor = nil
	style.H2.Prefix = "▌ "
	style.H3.Prefix = "› "
	style.H4.Prefix = ""
	style.H5.Prefix = ""
	style.H6.Prefix = ""
	return style
}
