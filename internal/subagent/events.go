package subagent

import "github.com/wt68/runcode/engine/tool"

// eventBridgeBuffer bounds the channel the child session writes tool events to.
const eventBridgeBuffer = 32

// startEventBridge returns a channel for a child session's ToolEvents and a stop
// function that closes it and waits for the forwarder to drain. The forwarder
// stamps every event from the sub-agent with parentID (the Task call's tool-use id)
// and agentName, then forwards it to the parent channel — so the UI can nest the
// sub-agent's tool cards and streamed text live under that Task card, rather than
// either hiding them or scattering them as top-level rows.
//
// Emits are non-blocking: under backpressure an event is dropped rather than
// stalling the sub-agent. When parent is nil the bridge still drains the child
// channel so the child never blocks writing events.
func startEventBridge(parent chan<- tool.Event, parentID, agentName string) (chan<- tool.Event, func()) {
	in := make(chan tool.Event, eventBridgeBuffer)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range in {
			if parent == nil {
				continue
			}
			event.ParentToolUseID = parentID
			event.AgentName = agentName
			select {
			case parent <- event:
			default:
			}
		}
	}()
	return in, func() {
		close(in)
		<-done
	}
}

// emitAgentDelta forwards a sub-agent's streamed assistant-text delta through the
// bridge (which stamps the parent attribution). Non-blocking: a dropped delta only
// costs a little live text, never a stall.
func emitAgentDelta(child chan<- tool.Event, delta string) {
	if child == nil || delta == "" {
		return
	}
	select {
	case child <- tool.Event{Type: tool.EventTypeAgentDelta, Message: delta}:
	default:
	}
}

// emitAgentUsage reports the sub-agent's total token spend and run time through the
// bridge, so the UI can show them on the Task card. Non-blocking, like the deltas.
func emitAgentUsage(child chan<- tool.Event, in, out, durMs int) {
	if child == nil || (in == 0 && out == 0 && durMs == 0) {
		return
	}
	select {
	case child <- tool.Event{Type: tool.EventTypeAgentUsage, InputTokens: in, OutputTokens: out, DurationMs: durMs}:
	default:
	}
}
