package ui

import (
	"fmt"
	"strings"
	"testing"
)

// resetMarkdownMemo 清空渲染缓存并丢弃复用的 renderer，让用例从冷态开始。
// 内容寻址的缓存被并发清空也只是多渲染一次，不影响其他并行用例的正确性。
func resetMarkdownMemo() {
	markdownMu.Lock()
	defer markdownMu.Unlock()
	markdownMemo = map[markdownMemoKey]string{}
	markdownRenderer = nil
	markdownRendererWidth = 0
}

// 复用同一个 glamour renderer 连续渲染、以及命中缓存返回的结果，必须与
// 每次新建 renderer 的冷渲染逐字节一致——这是渲染缓存不改变可见输出的保证。
func TestRenderMarkdownStableAcrossRendererReuseAndMemo(t *testing.T) {
	texts := []string{
		"# A\n\n```go\nx := 1\n```",
		"## B\n\n- one\n- two",
		"plain *emphasis* text",
	}

	baseline := make([]string, len(texts))
	for i, txt := range texts {
		resetMarkdownMemo() // 每条都用全新 renderer 冷渲染
		baseline[i] = renderMarkdown(txt, 60, false)
	}

	resetMarkdownMemo()
	for i, txt := range texts {
		if got := renderMarkdown(txt, 60, true); got != baseline[i] {
			t.Errorf("reused-renderer render #%d = %q, want cold render %q", i, got, baseline[i])
		}
	}
	for i, txt := range texts {
		if got := renderMarkdown(txt, 60, true); got != baseline[i] {
			t.Errorf("memo hit #%d = %q, want cold render %q", i, got, baseline[i])
		}
	}
}

// 宽度变化要换 renderer 重新折行，不能沿用旧宽度的缓存或 renderer。
func TestRenderMarkdownRewrapsOnWidthChange(t *testing.T) {
	resetMarkdownMemo()
	text := "one two three four five six seven eight nine ten eleven twelve"
	wide := renderMarkdown(text, 200, true)
	narrow := renderMarkdown(text, 24, true)
	if strings.Count(strings.TrimSpace(wide), "\n") >= strings.Count(strings.TrimSpace(narrow), "\n") {
		t.Fatalf("narrow render should wrap to more lines\nwide = %q\nnarrow = %q", wide, narrow)
	}
	if got := renderMarkdown(text, 24, true); got != narrow {
		t.Fatalf("re-render at 24 = %q, want cached %q", got, narrow)
	}
}

// 流式(未完结)消息不得写入缓存：每个增量都是新 key，缓存只会被灌满而永远不再命中。
func TestRenderMarkdownStreamingNotCached(t *testing.T) {
	resetMarkdownMemo()
	if out := renderMarkdown("# streaming partial", 60, false); out == "" {
		t.Fatal("streaming render should still produce output")
	}
	markdownMu.Lock()
	n := len(markdownMemo)
	markdownMu.Unlock()
	if n != 0 {
		t.Fatalf("memo size after streaming render = %d, want 0", n)
	}

	renderMarkdown("# done", 60, true)
	markdownMu.Lock()
	n = len(markdownMemo)
	markdownMu.Unlock()
	if n != 1 {
		t.Fatalf("memo size after completed render = %d, want 1", n)
	}
}

// 模拟一次流式增量触发的整屏刷新：40 条已完结的 markdown 消息 + 1 条流式尾巴。
// cold 子基准每次清空缓存与 renderer，即修复前"每个增量都全量重新解析"的成本；
// warm 子基准命中缓存，即修复后的稳态成本。
func BenchmarkRefreshTranscript(b *testing.B) {
	messages := make([]ChatMessage, 0, 41)
	for i := 0; i < 40; i++ {
		text := fmt.Sprintf("## Answer %d\n\nSome *rendered* prose with `code` and a list:\n\n- alpha %d\n- beta\n\n```go\nfmt.Println(%d)\n```", i, i, i)
		messages = append(messages, ChatMessage{Role: RoleAssistant, Text: text})
	}
	messages = append(messages, ChatMessage{Role: RoleAssistant, Text: "streaming tail", Streaming: true})

	b.Run("cold", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			resetMarkdownMemo()
			renderMessages(messages, 100)
		}
	})
	b.Run("warm", func(b *testing.B) {
		resetMarkdownMemo()
		renderMessages(messages, 100)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			renderMessages(messages, 100)
		}
	})
}
