package ui

import "testing"

// 宽度必须按终端列数计：中文一个字占 2 列。底部区域按"每行必不折行"做高度
// 预算，按 rune 计数会让含中文的行超宽折行、整块布局错位（审核问题 #7）。
func TestDisplayWidthCountsTerminalCells(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"中文", 4},
		{"fix/中文分支", 12},
	}
	for _, c := range cases {
		if got := displayWidth(c.in); got != c.want {
			t.Errorf("displayWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestTruncateByDisplayWidth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in    string
		width int
		want  string
	}{
		{"abcdef", 10, "abcdef"},    // 装得下不截断
		{"abcdef", 5, "abcd…"},      // ASCII 行为与改造前一致
		{"中文路径测试", 12, "中文路径测试"},    // 12 列恰好装下
		{"中文路径测试", 7, "中文路…"},       // 6 列内容 + 1 列省略号
		{"中文a", 5, "中文a"},           // 恰好 5 列，不截断
		{"中文abc", 5, "中文…"},         // 7 列截到 4 列内容 + 1 列省略号
		{"anything", 0, "anything"}, // width<=0 = 不限制（历史约定）
	}
	for _, c := range cases {
		if got := truncate(c.in, c.width); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.width, got, c.want)
		}
	}
	// 任何截断结果都不得超出列宽预算——这是不折行保证的核心。
	for _, in := range []string{"中文路径测试", "mixed中英文branch", "……………………"} {
		for width := 1; width <= 10; width++ {
			if got := truncate(in, width); displayWidth(got) > width {
				t.Errorf("truncate(%q, %d) = %q, display width %d exceeds budget", in, width, got, displayWidth(got))
			}
		}
	}
}

// 状态栏按列宽装配：含中文的部件超宽时要正确丢弃，而不是按 rune 低估宽度硬塞。
func TestCompactStatusLineUsesDisplayWidth(t *testing.T) {
	t.Parallel()
	parts := []string{"idle", "工作区名称很长的中文目录"}
	// rune 计数 = 4+3+12 = 19 会误判装得下；列宽 = 4+3+24 = 31 装不下,应只留 "idle"。
	if got := compactStatusLine(parts, 20); got != "idle" {
		t.Fatalf("compactStatusLine = %q, want %q", got, "idle")
	}
}
