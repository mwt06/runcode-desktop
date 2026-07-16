package desktop

import (
	"github.com/wt68/runcode/engine/permissions"
	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/pkg/protocol"
)

// This file is the single crossing point between the engine's internal event
// types and the wire protocol. Every field that should reach the frontend is
// copied explicitly, so an engine-side change never leaks onto the wire by
// accident — extending the wire is a deliberate edit here plus pkg/protocol.

// toolEventDTO converts an engine tool event to its wire form. The engine's
// Time field is intentionally dropped: the envelope's ts is the one clock.
func toolEventDTO(ev tool.Event) protocol.ToolEvent {
	return protocol.ToolEvent{
		Type:            string(ev.Type),
		ToolName:        ev.ToolName,
		ToolUseID:       ev.ToolUseID,
		ParentToolUseID: ev.ParentToolUseID,
		AgentName:       ev.AgentName,
		Input:           ev.Input,
		Message:         ev.Message,
		Data:            ev.Data,
		Files:           fileReferencesDTO(ev.Files),
		FilesTotal:      ev.FilesTotal,
		Output:          outputLinesDTO(ev.Output),
		OutputTotal:     ev.OutputTotal,
		OutputTruncated: ev.OutputTruncated,
		Image:           resultImageDTO(ev.Image),
		InputTokens:     ev.InputTokens,
		OutputTokens:    ev.OutputTokens,
		DurationMs:      ev.DurationMs,
	}
}

func fileReferencesDTO(refs []tool.FileReference) []protocol.FileReference {
	if len(refs) == 0 {
		return nil
	}
	out := make([]protocol.FileReference, len(refs))
	for i, r := range refs {
		out[i] = protocol.FileReference{Path: r.Path, Kind: string(r.Kind)}
	}
	return out
}

func outputLinesDTO(lines []tool.OutputLine) []protocol.OutputLine {
	if len(lines) == 0 {
		return nil
	}
	out := make([]protocol.OutputLine, len(lines))
	for i, l := range lines {
		out[i] = protocol.OutputLine{Stream: string(l.Stream), Text: l.Text}
	}
	return out
}

func resultImageDTO(img *tool.ResultImage) *protocol.ResultImage {
	if img == nil {
		return nil
	}
	return &protocol.ResultImage{MediaType: img.MediaType, Data: img.Data, URL: img.URL}
}

// approvalSummaryDTO converts the engine's approval classification to the wire
// subset a UI renders. Engine-internal fields (read-gate state, capability
// lists) deliberately do not cross.
func approvalSummaryDTO(s permissions.ApprovalSummary) protocol.ApprovalSummary {
	return protocol.ApprovalSummary{
		ToolName:        s.ToolName,
		Operation:       string(s.Operation),
		Risk:            string(s.Risk),
		ResourceTypes:   s.ResourceTypes,
		ResourceScope:   s.ResourceScope,
		ResourceCount:   s.ResourceCount,
		MutationKind:    s.MutationKind,
		CommandCategory: s.CommandCategory,
		CommandSummary:  s.CommandSummary,
		NetworkHost:     s.NetworkHost,
		MCPServer:       s.MCPServer,
		MCPTool:         s.MCPTool,
		PolicyRule:      s.PolicyRule,
	}
}
