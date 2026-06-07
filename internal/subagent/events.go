package subagent

import "github.com/wt68/runcode/pkg/tool"

// eventBridgeBuffer bounds the channel the child session writes tool events to.
const eventBridgeBuffer = 32

// startEventBridge returns a channel for a child session's ToolEvents and a stop
// function that closes it and waits for the forwarder to drain. The forwarder
// translates a sub-agent's tool activity into concise progress lines emitted to
// the parent channel with no tool name or id, so the parent executor attributes
// them to the Task call rather than spawning orphan UI rows for the child's tools.
//
// Emits are non-blocking: under backpressure a progress line is dropped rather
// than stalling the sub-agent. When parent is nil the bridge still drains the
// child channel so the child never blocks writing events.
func startEventBridge(parent chan<- tool.Event) (chan<- tool.Event, func()) {
	in := make(chan tool.Event, eventBridgeBuffer)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range in {
			if parent == nil {
				continue
			}
			progress, ok := childProgress(event)
			if !ok {
				continue
			}
			select {
			case parent <- progress:
			default:
			}
		}
	}()
	return in, func() {
		close(in)
		<-done
	}
}

// childProgress maps a child tool event to a parent-facing progress line, or
// reports ok=false when the event should not surface. Only the start and failure
// of a child tool are surfaced, which keeps the parent view to a running indicator
// without echoing the child's full output.
func childProgress(event tool.Event) (tool.Event, bool) {
	switch event.Type {
	case tool.EventTypeStarted:
		if event.ToolName == "" {
			return tool.Event{}, false
		}
		return tool.Event{Type: tool.EventTypeProgress, Message: "→ " + event.ToolName}, true
	case tool.EventTypeFailed:
		if event.ToolName == "" {
			return tool.Event{}, false
		}
		return tool.Event{Type: tool.EventTypeProgress, Message: "✗ " + event.ToolName}, true
	default:
		return tool.Event{}, false
	}
}
