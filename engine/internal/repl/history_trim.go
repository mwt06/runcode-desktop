package repl

import "gitlab.ouc-online.com.cn/aibase/agentloop/llm"

// trimMessagesForHistoryBudget bounds the conversation history sent to the
// provider (and committed back to memory) by a message-count budget while
// keeping the in-flight turn intact.
//
// max is the total message budget. The mandatory segment — the current user
// message at currentUserIndex and every assistant/tool message produced after
// it — is always preserved in full, even when it alone exceeds max, so an
// executing turn is never truncated. Whatever budget remains is filled with
// the most recent older turns, oldest-first, dropping the rest.
//
// max <= 0 disables trimming and returns a full clone. The returned slice is
// always a deep clone (never shares backing storage with the input) and the
// recomputed index of the current user message is returned alongside it.
func trimMessagesForHistoryBudget(messages []llm.Message, max int, currentUserIndex int) ([]llm.Message, int) {
	if max <= 0 {
		return cloneMessages(messages), currentUserIndex
	}
	if currentUserIndex < 0 || currentUserIndex >= len(messages) {
		return cloneMessages(messages), currentUserIndex
	}

	older := messages[:currentUserIndex]
	mandatory := messages[currentUserIndex:]

	// Budget left for older history after reserving the mandatory segment.
	// When the current turn alone exceeds max this goes <= 0 and no older
	// history is kept.
	remaining := max - len(mandatory)

	segments := splitHistorySegments(older)
	kept := make([][]llm.Message, 0, len(segments))
	keptCount := 0
	for i := len(segments) - 1; i >= 0; i-- {
		segment := segments[i]
		// Stop at the first turn that would break tool pairing or overflow the
		// budget: keeping older turns past a gap would send a discontinuous
		// conversation to the provider.
		if !validHistorySegment(segment) {
			break
		}
		if keptCount+len(segment) > remaining {
			break
		}
		kept = append(kept, segment)
		keptCount += len(segment)
	}

	trimmed := make([]llm.Message, 0, keptCount+len(mandatory))
	for i := len(kept) - 1; i >= 0; i-- {
		trimmed = append(trimmed, kept[i]...)
	}
	newUserIndex := len(trimmed)
	trimmed = append(trimmed, mandatory...)

	return cloneMessages(trimmed), newUserIndex
}

// splitHistorySegments groups messages into per-turn segments delimited by
// RoleUser boundaries. Each segment begins at a user message (a normal,
// fully-paired turn from committed history); a leading non-user message would
// produce a segment that validHistorySegment rejects.
func splitHistorySegments(messages []llm.Message) [][]llm.Message {
	var segments [][]llm.Message
	start := 0
	for i, message := range messages {
		if i > start && message.Role == llm.RoleUser {
			segments = append(segments, messages[start:i])
			start = i
		}
	}
	if start < len(messages) {
		segments = append(segments, messages[start:])
	}
	return segments
}

// validHistorySegment reports whether a turn segment can be sent on its own
// without orphaning tool_use/tool_result pairs. A valid segment starts with a
// user message and every assistant tool_use is immediately followed by a tool
// message whose results match the requested tool-use IDs exactly.
func validHistorySegment(segment []llm.Message) bool {
	if len(segment) == 0 || segment[0].Role != llm.RoleUser {
		return false
	}
	for i := 0; i < len(segment); i++ {
		message := segment[i]
		switch message.Role {
		case llm.RoleTool:
			// Reached only when this tool message has no preceding assistant
			// tool_use (paired ones are consumed below) — an orphan result.
			return false
		case llm.RoleAssistant:
			if len(toolUseIDs(message)) == 0 {
				continue
			}
			if i+1 >= len(segment) || segment[i+1].Role != llm.RoleTool {
				return false
			}
			if !matchingToolResults(message, segment[i+1]) {
				return false
			}
			i++ // consume the paired tool message
		}
	}
	return true
}

// matchingToolResults reports whether the tool message carries exactly one
// result per tool_use requested by the assistant message.
func matchingToolResults(assistant llm.Message, toolMessage llm.Message) bool {
	uses := toolUseIDs(assistant)
	results := toolResultIDs(toolMessage)
	if len(uses) != len(results) {
		return false
	}
	for id := range uses {
		if _, ok := results[id]; !ok {
			return false
		}
	}
	return true
}

func toolUseIDs(message llm.Message) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, block := range message.Content {
		if block.Type == llm.ContentBlockTypeToolUse && block.ID != "" {
			ids[block.ID] = struct{}{}
		}
	}
	return ids
}

func toolResultIDs(message llm.Message) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, block := range message.Content {
		if block.Type == llm.ContentBlockTypeToolResult && block.ToolUseID != "" {
			ids[block.ToolUseID] = struct{}{}
		}
	}
	return ids
}
