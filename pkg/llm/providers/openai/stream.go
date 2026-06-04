package openai

import (
	"sync"

	"github.com/wt68/runcode/pkg/llm"
)

// streamState rebuilds OpenAI's flat streaming deltas (a running `content`
// string plus indexed `tool_calls` fragments) into runcode's block-structured
// events: each text or tool_use block gets a stable index with start/delta/stop,
// and tool_call argument fragments accumulate into the block's input JSON. It is
// a pure transducer so it can be unit-tested without goroutines.
type streamState struct {
	started   bool
	textIndex int
	toolIndex map[int]int // OpenAI tool_calls index -> llm block index
	order     []int       // llm block indexes in the order they started
	next      int         // next llm block index to assign
	finish    string
	usage     *llm.Usage
}

func newStreamState() *streamState {
	return &streamState{textIndex: -1, toolIndex: map[int]int{}}
}

// chunk converts one streamed payload into zero or more neutral events.
func (s *streamState) chunk(c chatChunk) []llm.StreamEvent {
	var events []llm.StreamEvent
	if !s.started {
		s.started = true
		events = append(events, llm.StreamEvent{Type: llm.StreamEventTypeMessageStart})
	}
	if c.Usage != nil {
		s.usage = &llm.Usage{
			InputTokens:  c.Usage.PromptTokens,
			OutputTokens: c.Usage.CompletionTokens,
		}
	}
	for _, choice := range c.Choices {
		events = append(events, s.delta(choice.Delta)...)
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			s.finish = *choice.FinishReason
		}
	}
	return events
}

func (s *streamState) delta(d chatDelta) []llm.StreamEvent {
	var events []llm.StreamEvent
	if d.Content != "" {
		if s.textIndex < 0 {
			s.textIndex = s.assign()
			events = append(events, llm.StreamEvent{
				Type:  llm.StreamEventTypeContentBlockStart,
				Index: s.textIndex,
				Block: &llm.ContentBlock{Type: llm.ContentBlockTypeText},
			})
		}
		events = append(events, llm.StreamEvent{
			Type:  llm.StreamEventTypeContentBlockDelta,
			Index: s.textIndex,
			Delta: &llm.ContentDelta{Text: d.Content},
		})
	}
	for _, call := range d.ToolCalls {
		llmIndex, ok := s.toolIndex[call.Index]
		if !ok {
			llmIndex = s.assign()
			s.toolIndex[call.Index] = llmIndex
			events = append(events, llm.StreamEvent{
				Type:  llm.StreamEventTypeContentBlockStart,
				Index: llmIndex,
				Block: &llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: call.ID, Name: call.Function.Name},
			})
		}
		if call.Function.Arguments != "" {
			events = append(events, llm.StreamEvent{
				Type:  llm.StreamEventTypeContentBlockDelta,
				Index: llmIndex,
				Delta: &llm.ContentDelta{InputJSON: []byte(call.Function.Arguments)},
			})
		}
	}
	return events
}

func (s *streamState) assign() int {
	index := s.next
	s.next++
	s.order = append(s.order, index)
	return index
}

// finishEvents closes every started block and emits the terminal message_stop.
func (s *streamState) finishEvents() []llm.StreamEvent {
	events := make([]llm.StreamEvent, 0, len(s.order)+1)
	for _, index := range s.order {
		events = append(events, llm.StreamEvent{Type: llm.StreamEventTypeContentBlockStop, Index: index})
	}
	events = append(events, llm.StreamEvent{
		Type:       llm.StreamEventTypeMessageStop,
		StopReason: mapFinishReason(s.finish, len(s.toolIndex) > 0),
		Usage:      s.usage,
	})
	return events
}

func mapFinishReason(reason string, hasTools bool) llm.StopReason {
	switch reason {
	case "stop":
		return llm.StopReasonEndTurn
	case "length":
		return llm.StopReasonMaxTokens
	case "tool_calls", "function_call":
		return llm.StopReasonToolUse
	case "":
		if hasTools {
			return llm.StopReasonToolUse
		}
		return llm.StopReasonEndTurn
	default:
		// content_filter and any non-standard reason: report a completed turn
		// rather than leak a token the session loop does not understand.
		return llm.StopReasonEndTurn
	}
}

// stream adapts an sseStream into an llm.Stream, draining it on a goroutine.
type stream struct {
	sse       sseStream
	events    chan llm.StreamEvent
	done      chan struct{}
	cancel    chan struct{}
	closeOnce sync.Once
	closeErr  error
	mu        sync.Mutex
	err       error
}

func newStream(sse sseStream) llm.Stream {
	wrapped := &stream{
		sse:    sse,
		events: make(chan llm.StreamEvent),
		done:   make(chan struct{}),
		cancel: make(chan struct{}),
	}
	go wrapped.run()
	return wrapped
}

func (s *stream) Events() <-chan llm.StreamEvent { return s.events }

func (s *stream) Err() error {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *stream) Close() error {
	s.closeOnce.Do(func() {
		close(s.cancel)
		s.closeErr = s.sse.Close()
	})
	return s.closeErr
}

func (s *stream) run() {
	defer close(s.events)
	defer close(s.done)

	state := newStreamState()
	for s.sse.Next() {
		for _, event := range state.chunk(s.sse.Current()) {
			if !s.send(event) {
				return
			}
		}
	}
	if err := s.sse.Err(); err != nil {
		s.setErr(err)
		return
	}
	for _, event := range state.finishEvents() {
		if !s.send(event) {
			return
		}
	}
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
