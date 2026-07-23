package ui

// 工具事件的接收侧:把引擎的 tool.Event 流归并进 ToolProgress,并在入口处净化
// 一切要显示的字符串。渲染只读这里产出的结构,不再碰原始事件。

import (
	"path/filepath"
	"strings"
	"unicode"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

const (
	// 单个工具最多保留多少条文件引用与输出行——长回合的工具输出不能无界增长。
	maxToolStoredFiles  = 50
	maxToolStoredOutput = 50
	// 显示用的截断宽度:输出行与文件路径各自的上限。
	toolLineMaxWidth     = 200
	toolFilePathMaxWidth = 96
)

func (m *Model) applyToolEvent(event tool.Event) {
	if event.Type == "" {
		return
	}
	key := toolProgressKey(event)
	idx, ok := m.toolMessages[key]
	if !ok || m.toolProgressAt(idx, key) == nil {
		progress := &ToolProgress{
			ToolName:  event.ToolName,
			ToolUseID: event.ToolUseID,
			Status:    ToolStatusRunning,
		}
		idx = m.toolBatchMessageIndex()
		m.messages[idx].Tools = append(m.messages[idx].Tools, progress)
		m.toolMessages[key] = idx
	}
	progress := m.toolProgressAt(idx, key)
	if progress == nil {
		return
	}
	if progress.ToolName == "" {
		progress.ToolName = event.ToolName
	}
	if progress.ToolUseID == "" {
		progress.ToolUseID = event.ToolUseID
	}
	if progress.StartedAt.IsZero() && !event.Time.IsZero() {
		progress.StartedAt = event.Time
	}
	appendToolFileReferences(progress, event.Files, event.FilesTotal)
	appendToolOutput(progress, event.Output, event.OutputTotal, event.OutputTruncated)
	message := toolEventMessage(event)
	switch event.Type {
	case tool.EventTypeStarted:
		progress.Status = ToolStatusRunning
		progress.Message = message
		if progress.StartedAt.IsZero() {
			progress.StartedAt = event.Time
		}
	case tool.EventTypeProgress, tool.EventTypeOutput:
		progress.Status = ToolStatusRunning
		progress.Message = message
	case tool.EventTypeCompleted:
		progress.Status = ToolStatusCompleted
		progress.Message = message
		progress.FinishedAt = event.Time
	case tool.EventTypeFailed:
		progress.Status = ToolStatusFailed
		progress.Message = message
		progress.FinishedAt = event.Time
	}
}

func (m *Model) toolBatchMessageIndex() int {
	m.closeCurrentAssistantForToolEvent()
	if len(m.messages) > 0 {
		idx := len(m.messages) - 1
		if m.messages[idx].Role == RoleTool {
			return idx
		}
	}
	m.messages = append(m.messages, ChatMessage{Role: RoleTool})
	return len(m.messages) - 1
}

func (m *Model) toolProgressAt(idx int, key string) *ToolProgress {
	if idx < 0 || idx >= len(m.messages) || m.messages[idx].Role != RoleTool {
		return nil
	}
	message := &m.messages[idx]
	if message.Tool != nil {
		return message.Tool
	}
	for _, progress := range message.Tools {
		if progress != nil && toolProgressKeyFromValues(progress.ToolName, progress.ToolUseID) == key {
			return progress
		}
	}
	return nil
}

func (m *Model) closeCurrentAssistantForToolEvent() {
	if m.currentAssistant < 0 || m.currentAssistant >= len(m.messages) {
		m.currentAssistant = -1
		return
	}
	current := m.messages[m.currentAssistant]
	if current.Role != RoleAssistant || !current.Streaming {
		m.currentAssistant = -1
		return
	}
	if strings.TrimSpace(current.Text) == "" {
		removed := m.currentAssistant
		m.messages = append(m.messages[:removed], m.messages[removed+1:]...)
		m.reindexToolMessagesAfterRemoval(removed)
	} else {
		m.messages[m.currentAssistant].Streaming = false
	}
	m.currentAssistant = -1
}

func (m *Model) reindexToolMessagesAfterRemoval(removed int) {
	for key, idx := range m.toolMessages {
		if idx == removed {
			delete(m.toolMessages, key)
			continue
		}
		if idx > removed {
			m.toolMessages[key] = idx - 1
		}
	}
}

func toolProgressKey(event tool.Event) string {
	return toolProgressKeyFromValues(event.ToolName, event.ToolUseID)
}

func toolProgressKeyFromValues(toolName string, toolUseID string) string {
	if toolUseID != "" {
		return "id:" + toolUseID
	}
	if toolName != "" {
		return "name:" + toolName
	}
	return "unknown"
}

func appendToolFileReferences(progress *ToolProgress, refs []tool.FileReference, total int) {
	if progress == nil {
		return
	}
	seen := make(map[string]struct{}, len(progress.Files)+len(refs))
	for _, file := range progress.Files {
		seen[file.Path] = struct{}{}
	}
	for _, ref := range refs {
		path, ok := safeToolFilePath(ref.Path)
		if !ok {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		progress.Files = append(progress.Files, ToolFileReference{Path: path, Kind: string(ref.Kind)})
		if len(progress.Files) >= maxToolStoredFiles {
			break
		}
	}
	if len(progress.Files) > 0 && total > progress.FilesTotal {
		progress.FilesTotal = total
	}
	if progress.FilesTotal < len(progress.Files) {
		progress.FilesTotal = len(progress.Files)
	}
}

// safeToolFilePath 只接受工作区内的相对路径:绝对路径、盘符、.. 回退、控制字符
// 一律拒绝——这些字符串会直接进终端,不能带转义序列或越界路径。
func safeToolFilePath(value string) (string, bool) {
	value = strings.TrimSpace(filepath.ToSlash(value))
	if value == "" || filepath.IsAbs(value) || strings.HasPrefix(value, "/") || strings.Contains(value, ":") {
		return "", false
	}
	segments := strings.Split(value, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
		for _, r := range segment {
			if r == '\n' || r == '\r' || unicode.IsControl(r) {
				return "", false
			}
		}
	}
	return truncate(strings.Join(segments, "/"), toolFilePathMaxWidth), true
}

func toolEventMessage(event tool.Event) string {
	message := strings.TrimSpace(event.Message)
	if message != "" {
		return message
	}
	switch event.Type {
	case tool.EventTypeStarted:
		return "started"
	case tool.EventTypeProgress:
		return "running"
	case tool.EventTypeOutput:
		return "output"
	case tool.EventTypeCompleted:
		return "completed"
	case tool.EventTypeFailed:
		return "failed"
	default:
		return string(event.Type)
	}
}

func appendToolOutput(progress *ToolProgress, lines []tool.OutputLine, total int, truncated bool) {
	if progress == nil || len(lines) == 0 {
		return
	}
	for _, line := range lines {
		if len(progress.Output) >= maxToolStoredOutput {
			truncated = true
			break
		}
		progress.Output = append(progress.Output, ToolOutputLine{Stream: string(line.Stream), Text: safeOutputText(line.Text)})
	}
	if total > progress.OutputTotal {
		progress.OutputTotal = total
	}
	if progress.OutputTotal < len(progress.Output) {
		progress.OutputTotal = len(progress.Output)
	}
	if truncated {
		progress.OutputTruncated = true
	}
}

// safeOutputText 把一行工具输出压成单行可打印文本:制表符换空格,换行与控制字符
// 剔除(否则会打乱 TUI 布局或注入转义序列),再按显示宽度截断。
func safeOutputText(text string) string {
	text = strings.ReplaceAll(text, "\t", "    ")
	var b strings.Builder
	for _, r := range text {
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return truncate(b.String(), toolLineMaxWidth)
}
