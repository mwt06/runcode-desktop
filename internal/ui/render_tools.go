package ui

// 工具进度的汇总与渲染。汇总(summarizeToolProgress 一族)是纯数据变换:把一批
// ToolProgress 按 名称+状态 合并成若干 summary,顺序稳定;渲染只在 summary 上做
// 排版与折叠。两段刻意分开——前者可单测,后者只关心样式。

import (
	"fmt"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// 折叠态与展开态(ctrl+o)各自显示多少条文件与输出行。
const (
	collapsedToolFileLimit = 3
	expandedToolFileLimit  = 20
)

const (
	collapsedToolOutputLimit = 5
	expandedToolOutputLimit  = 20
)

func hasToolProgress(message ChatMessage) bool {
	return message.Tool != nil || len(message.Tools) > 0
}

func renderToolProgress(progress ToolProgress) string {
	return renderToolProgressGroup([]ChatMessage{{Role: RoleTool, Tool: &progress}}, false)
}

func renderToolProgressGroup(messages []ChatMessage, expanded bool) string {
	summaries := summarizeToolProgress(messages)
	if len(summaries) == 0 {
		return mutedStyle.Render("Tools")
	}
	label := mutedStyle.Render("Tools")
	lines := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		lines = append(lines, renderToolProgressSummary(summary, expanded)...)
	}
	return fmt.Sprintf("%s\n%s", label, indent(strings.Join(lines, "\n"), "  "))
}

type toolProgressSummary struct {
	name        string
	status      ToolStatus
	message     string
	count       int
	files       []ToolFileReference
	filesTotal  int
	output      []ToolOutputLine
	outputTotal int
}

func toolProgresses(message ChatMessage) []*ToolProgress {
	if len(message.Tools) > 0 {
		return message.Tools
	}
	if message.Tool != nil {
		return []*ToolProgress{message.Tool}
	}
	return nil
}

// summarizeToolProgress 按 名称+状态 归并,并保持首次出现的顺序——同一工具的多次
// 调用折成一行(Read 3 files · done),而先后关系不被打乱。
func summarizeToolProgress(messages []ChatMessage) []toolProgressSummary {
	order := []string{}
	byKey := map[string]int{}
	summaries := []toolProgressSummary{}
	for _, message := range messages {
		for _, progress := range toolProgresses(message) {
			name := progress.ToolName
			if name == "" {
				name = "unknown"
			}
			status := progress.Status
			if status == "" {
				status = ToolStatusRunning
			}
			key := name + "\x00" + string(status)
			idx, ok := byKey[key]
			if !ok {
				byKey[key] = len(summaries)
				order = append(order, key)
				summaries = append(summaries, toolProgressSummary{name: name, status: status})
				idx = len(summaries) - 1
			}
			summary := &summaries[idx]
			summary.count++
			if strings.TrimSpace(progress.Message) != "" {
				summary.message = strings.TrimSpace(progress.Message)
			}
			appendSummaryFiles(summary, progress.Files, progress.FilesTotal)
			appendSummaryOutput(summary, progress.Output, progress.OutputTotal)
		}
	}
	ordered := make([]toolProgressSummary, 0, len(order))
	for _, key := range order {
		ordered = append(ordered, summaries[byKey[key]])
	}
	return ordered
}

func appendSummaryFiles(summary *toolProgressSummary, files []ToolFileReference, total int) {
	if summary == nil {
		return
	}
	seen := make(map[string]struct{}, len(summary.files)+len(files))
	for _, file := range summary.files {
		seen[file.Path] = struct{}{}
	}
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		summary.files = append(summary.files, file)
	}
	if total > summary.filesTotal {
		summary.filesTotal = total
	}
	if summary.filesTotal < len(summary.files) {
		summary.filesTotal = len(summary.files)
	}
}

func appendSummaryOutput(summary *toolProgressSummary, lines []ToolOutputLine, total int) {
	if summary == nil || len(lines) == 0 {
		return
	}
	summary.output = append(summary.output, lines...)
	if total > summary.outputTotal {
		summary.outputTotal = total
	}
	if summary.outputTotal < len(summary.output) {
		summary.outputTotal = len(summary.output)
	}
}

func renderToolProgressSummary(summary toolProgressSummary, expanded bool) []string {
	status := toolStatusLabel(summary.status)
	if summary.status == ToolStatusFailed {
		status = errorStyle.Render(status)
	}
	fileCount := summary.filesTotal
	if fileCount == 0 {
		fileCount = len(summary.files)
	}
	line := toolSummaryLabel(summary, fileCount) + " · " + status
	if message := displayToolMessage(summary); message != "" {
		line += " · " + truncate(message, toolLineMaxWidth)
	}
	hasMore := fileCount > collapsedToolFileLimit || summary.outputTotal > collapsedToolOutputLimit
	if hasMore && !expanded {
		line += " " + mutedStyle.Render("(ctrl+o to expand)")
	} else if hasMore && expanded {
		line += " " + mutedStyle.Render("(ctrl+o to collapse)")
	}
	lines := []string{line}
	lines = append(lines, renderToolFileLines(summary, expanded)...)
	lines = append(lines, renderToolOutputLines(summary, expanded)...)
	return lines
}

func renderToolOutputLines(summary toolProgressSummary, expanded bool) []string {
	if len(summary.output) == 0 {
		return nil
	}
	limit := collapsedToolOutputLimit
	if expanded {
		limit = expandedToolOutputLimit
	}
	shown := min(len(summary.output), limit)
	total := summary.outputTotal
	if total < len(summary.output) {
		total = len(summary.output)
	}
	remaining := total - shown
	if remaining < 0 {
		remaining = 0
	}
	guide := mutedStyle.Render("│ ")
	lines := make([]string, 0, shown+1)
	for i := 0; i < shown; i++ {
		lines = append(lines, guide+styleToolOutputLine(summary.output[i]))
	}
	if remaining > 0 {
		lines = append(lines, guide+mutedStyle.Render(fmt.Sprintf("+%d more lines", remaining)))
	}
	return lines
}

func styleToolOutputLine(line ToolOutputLine) string {
	text := line.Text
	switch tool.OutputStream(line.Stream) {
	case tool.OutputStreamDiffAdd:
		return diffAddStyle.Render(text)
	case tool.OutputStreamDiffDel, tool.OutputStreamStderr:
		return diffDelStyle.Render(text)
	case tool.OutputStreamDiffContext, tool.OutputStreamInfo:
		return mutedStyle.Render(text)
	default:
		return text
	}
}

func toolSummaryLabel(summary toolProgressSummary, fileCount int) string {
	if fileCount > 0 {
		unit := "files"
		if fileCount == 1 {
			unit = "file"
		}
		return fmt.Sprintf("%s %d %s", summary.name, fileCount, unit)
	}
	return fmt.Sprintf("%s ×%d", summary.name, summary.count)
}

func renderToolFileLines(summary toolProgressSummary, expanded bool) []string {
	if len(summary.files) == 0 {
		return nil
	}
	limit := collapsedToolFileLimit
	if expanded {
		limit = expandedToolFileLimit
	}
	shown := min(len(summary.files), limit)
	remaining := summary.filesTotal - shown
	if remaining < 0 {
		remaining = 0
	}
	lines := make([]string, 0, shown+1)
	for i := 0; i < shown; i++ {
		connector := "├"
		if i == shown-1 && remaining == 0 {
			connector = "└"
		}
		lines = append(lines, fmt.Sprintf("%s %s", connector, summary.files[i].Path))
	}
	if remaining > 0 {
		lines = append(lines, fmt.Sprintf("└ +%d more", remaining))
	}
	return lines
}

func toolStatusLabel(status ToolStatus) string {
	switch status {
	case ToolStatusCompleted:
		return "done"
	case ToolStatusFailed:
		return "failed"
	case ToolStatusRunning:
		return "running"
	default:
		return string(status)
	}
}

// displayToolMessage 丢掉与状态标签重复的套话(started/completed/…),只留下真正
// 有信息量的那句,免得每行都是 "done · completed"。
func displayToolMessage(summary toolProgressSummary) string {
	message := strings.TrimSpace(summary.message)
	if message == "" {
		return ""
	}
	switch message {
	case "started", "running", "output", "completed", "failed", "matched files", toolStatusLabel(summary.status):
		return ""
	default:
		return message
	}
}
