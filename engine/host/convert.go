package host

import (
	"strings"

	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/engine/permissions"
	"github.com/wt68/runcode/engine/protocol"
	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/engine/turn"
)

// This file is the host's single crossing point between the engine's internal
// event types and the wire protocol (a deliberate copy of the desktop's
// converter; the desktop keeps its own until it is rebased onto this package).
// Every field that should reach a frontend is copied explicitly, so an
// engine-side change never leaks onto the wire by accident — extending the
// wire is a deliberate edit here plus pkg/protocol.

// ToolEventDTO converts an engine tool event to its wire form. The engine's
// Time field is intentionally dropped: the envelope's ts is the one clock.
func ToolEventDTO(ev tool.Event) protocol.ToolEvent {
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
		Output:          OutputLinesDTO(ev.Output),
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

// OutputLinesDTO converts bounded tool output lines to their wire form.
func OutputLinesDTO(lines []tool.OutputLine) []protocol.OutputLine {
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

// ApprovalSummaryDTO converts the engine's approval classification to the wire
// subset a UI renders. Engine-internal fields (read-gate state, capability
// lists) deliberately do not cross.
func ApprovalSummaryDTO(s permissions.ApprovalSummary) protocol.ApprovalSummary {
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

// turnEndFromResult maps a turn result to the flat TurnEnd payload. durMs is
// the turn's measured wall-clock time.
func turnEndFromResult(r turn.Result, durMs int) protocol.TurnEnd {
	in, out := 0, 0
	for _, u := range r.Usages {
		if u != nil {
			in += u.InputTokens
			out += u.OutputTokens
		}
	}
	// The final request's input tokens are the current context occupancy (each
	// ReAct iteration resends the growing history, so the last is the fullest).
	ctx := 0
	if r.FinalUsage != nil {
		ctx = r.FinalUsage.InputTokens
	}
	return protocol.TurnEnd{
		Text:            llm.TextContent(r.FinalAssistant),
		StopReason:      string(r.FinalStopReason),
		Iterations:      r.Iterations,
		ToolResultCount: len(r.ToolResults),
		InputTokens:     in,
		OutputTokens:    out,
		ContextTokens:   ctx,
		Stopped:         r.Stopped,
		DurationMs:      durMs,
	}
}

// warnWriter adapts a session's emit function to the io.Writer the engine's
// startup diagnostics expect, emitting each write as a warning event.
type warnWriter struct {
	emit func(event string, payload any)
}

func (w warnWriter) Write(p []byte) (int, error) {
	if msg := strings.TrimRight(string(p), "\n"); msg != "" {
		w.emit(protocol.EventWarning, protocol.Warning{Message: msg})
	}
	return len(p), nil
}
