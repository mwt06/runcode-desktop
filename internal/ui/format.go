package ui

// 状态行与卡片用到的短格式化函数。都是纯函数:给定输入只产出显示字符串,不碰
// Model 也不碰样式。

import (
	"fmt"
	"path/filepath"
	"strings"
)

func maxZero(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func indent(text string, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func modelLabel(model string) string {
	if strings.TrimSpace(model) == "" {
		return "-"
	}
	return strings.TrimSpace(model)
}

func permissionLabel(mode string) string {
	if strings.TrimSpace(mode) == "" {
		return "permission:-"
	}
	return strings.TrimSpace(mode)
}

func formatContext(inputTokens int, outputTokens int, maxContextTokens int) string {
	if inputTokens <= 0 && outputTokens <= 0 {
		return "-"
	}
	if maxContextTokens > 0 && inputTokens > 0 {
		return fmt.Sprintf("total %s/%s", formatTokenCount(inputTokens), formatTokenCount(maxContextTokens))
	}
	if outputTokens > 0 {
		return fmt.Sprintf("total %s in / %s out", formatTokenCount(inputTokens), formatTokenCount(outputTokens))
	}
	return "total " + formatTokenCount(inputTokens) + " in"
}

func formatTokenCount(tokens int) string {
	if tokens <= 0 {
		return "0"
	}
	if tokens < 1000 {
		return fmt.Sprintf("%d", tokens)
	}
	whole := tokens / 1000
	fraction := (tokens % 1000) / 100
	if fraction == 0 {
		return fmt.Sprintf("%dk", whole)
	}
	return fmt.Sprintf("%d.%dk", whole, fraction)
}

func formatDiffStats(stats DiffStats) string {
	if !stats.Available {
		return ""
	}
	if stats.Insertions == 0 && stats.Deletions == 0 {
		if stats.FilesChanged > 0 {
			return fmt.Sprintf("(%d files)", stats.FilesChanged)
		}
		return "clean"
	}
	return fmt.Sprintf("(+%d,-%d)", stats.Insertions, stats.Deletions)
}

func formatThinking(mode string, scenario string, confidence string) string {
	scenario = strings.TrimSpace(scenario)
	confidence = strings.TrimSpace(confidence)
	if scenario != "" {
		if confidence != "" {
			return scenario + " " + confidence
		}
		return scenario
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "off"
	}
	return mode
}

func shortSessionID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	const maxSessionIDRunes = 12
	return truncate(id, maxSessionIDRunes)
}

// compactStatusLine 把状态项拼成一行,放不下就从尾部逐个丢——右侧的次要信息
// (session/transcript)先让位,左侧的模型与上下文占用最后才被裁掉。
func compactStatusLine(parts []string, width int) string {
	parts = nonEmpty(parts)
	for len(parts) > 0 {
		line := strings.Join(parts, " | ")
		if width <= 0 || displayWidth(line) <= width {
			return line
		}
		parts = parts[:len(parts)-1]
	}
	return truncate("runcode", width)
}

func shortPath(value string) string {
	if value == "" {
		return "cwd:-"
	}
	base := filepath.Base(value)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return value
	}
	return base
}

func transcriptLabel(status Status) string {
	if status.Transcript == "" || status.Transcript == "off" {
		return "off"
	}
	if status.SessionID != "" {
		return status.Transcript + ":" + status.SessionID
	}
	return status.Transcript
}
