package anthropic

import (
	"sync"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/wt68/runcode/pkg/llm"
)

type stream struct {
	sdkStream sdkStream
	events    chan llm.StreamEvent
	done      chan struct{}
	cancel    chan struct{}
	closeOnce sync.Once
	closeErr  error
	mu        sync.Mutex
	err       error
}

func newStream(s sdkStream) llm.Stream {
	wrapped := &stream{
		sdkStream: s,
		events:    make(chan llm.StreamEvent),
		done:      make(chan struct{}),
		cancel:    make(chan struct{}),
	}
	go wrapped.run()
	return wrapped
}

func (s *stream) Events() <-chan llm.StreamEvent {
	return s.events
}

func (s *stream) Err() error {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *stream) Close() error {
	s.closeOnce.Do(func() {
		close(s.cancel)
		s.closeErr = s.sdkStream.Close()
	})
	return s.closeErr
}

func (s *stream) run() {
	defer close(s.events)
	defer close(s.done)

	var stopReason llm.StopReason
	var usage *llm.Usage
	for s.sdkStream.Next() {
		event := s.sdkStream.Current()
		converted, nextStopReason, nextUsage := convertStreamEvent(event, stopReason, usage)
		if nextStopReason != "" {
			stopReason = nextStopReason
		}
		if nextUsage != nil {
			usage = nextUsage
		}
		if converted.Type != "" && !s.send(converted) {
			return
		}
	}
	s.setErr(s.sdkStream.Err())
}

func (s *stream) send(event llm.StreamEvent) bool {
	select {
	case s.events <- event:
		return true
	case <-s.cancel:
		return false
	}
}

func (s *stream) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func convertStreamEvent(event sdk.MessageStreamEventUnion, stopReason llm.StopReason, usage *llm.Usage) (llm.StreamEvent, llm.StopReason, *llm.Usage) {
	switch variant := event.AsAny().(type) {
	case sdk.MessageStartEvent:
		return llm.StreamEvent{Type: llm.StreamEventTypeMessageStart, ProviderData: variant}, "", nil
	case sdk.ContentBlockStartEvent:
		return llm.StreamEvent{
			Type:         llm.StreamEventTypeContentBlockStart,
			Index:        int(variant.Index),
			Block:        convertStartedBlock(variant.ContentBlock),
			ProviderData: variant,
		}, "", nil
	case sdk.ContentBlockDeltaEvent:
		return llm.StreamEvent{
			Type:         llm.StreamEventTypeContentBlockDelta,
			Index:        int(variant.Index),
			Delta:        convertDelta(variant.Delta),
			ProviderData: variant,
		}, "", nil
	case sdk.ContentBlockStopEvent:
		return llm.StreamEvent{Type: llm.StreamEventTypeContentBlockStop, Index: int(variant.Index), ProviderData: variant}, "", nil
	case sdk.MessageDeltaEvent:
		return llm.StreamEvent{}, mapStopReason(string(variant.Delta.StopReason)), convertUsage(variant.Usage)
	case sdk.MessageStopEvent:
		return llm.StreamEvent{Type: llm.StreamEventTypeMessageStop, StopReason: stopReason, Usage: usage, ProviderData: variant}, "", nil
	default:
		return llm.StreamEvent{}, "", nil
	}
}

func convertStartedBlock(block sdk.ContentBlockStartEventContentBlockUnion) *llm.ContentBlock {
	switch block.Type {
	case "text":
		return &llm.ContentBlock{Type: llm.ContentBlockTypeText, Text: block.Text}
	case "tool_use":
		return &llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: block.ID, Name: block.Name}
	case "thinking":
		return &llm.ContentBlock{Type: llm.ContentBlockTypeThinking, Text: block.Thinking, Signature: block.Signature}
	default:
		return &llm.ContentBlock{Type: llm.ContentBlockType(block.Type)}
	}
}

func convertDelta(delta sdk.RawContentBlockDeltaUnion) *llm.ContentDelta {
	if delta.Type == "" {
		return nil
	}
	return &llm.ContentDelta{
		Text:      delta.Text,
		InputJSON: []byte(delta.PartialJSON),
		Thinking:  delta.Thinking,
		Signature: delta.Signature,
	}
}

func convertUsage(usage sdk.MessageDeltaUsage) *llm.Usage {
	return &llm.Usage{
		InputTokens:              int(usage.InputTokens),
		OutputTokens:             int(usage.OutputTokens),
		CacheCreationInputTokens: int(usage.CacheCreationInputTokens),
		CacheReadInputTokens:     int(usage.CacheReadInputTokens),
	}
}

func mapStopReason(reason string) llm.StopReason {
	switch reason {
	case "":
		return ""
	case string(llm.StopReasonEndTurn):
		return llm.StopReasonEndTurn
	case string(llm.StopReasonMaxTokens):
		return llm.StopReasonMaxTokens
	case string(llm.StopReasonToolUse):
		return llm.StopReasonToolUse
	case string(llm.StopReasonStopSequence):
		return llm.StopReasonStopSequence
	default:
		return llm.StopReason(reason)
	}
}
