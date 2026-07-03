package openai

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/wt68/runcode/pkg/llm"
)

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

// streamState rebuilds OpenAI's flat streaming deltas (a running `content`
// string plus indexed `tool_calls` fragments) into runcode's block-structured
// events: each text or tool_use block gets a stable index with start/delta/stop,
// and tool_call argument fragments accumulate into the block's input JSON. It is
// a pure transducer so it can be unit-tested without goroutines.
type streamState struct {
	started       bool
	textIndex     int
	thinkingIndex int
	toolIndex     map[int]int // OpenAI tool_calls index -> llm block index
	order         []int       // llm block indexes in the order they started
	next          int         // next llm block index to assign
	finish        string
	usage         *llm.Usage
	// inThink/pending drive the inline <think>…</think> splitter: content is
	// scanned across chunk boundaries so a tag split between chunks is not missed
	// and partial-tag prefixes are held back rather than emitted as answer text.
	inThink bool
	pending string
	// sawReasoning records that the stream delivered reasoning on the explicit
	// reasoning_content/reasoning channel. Some gateways (e.g. Qwen on some
	// OpenAI-compatible endpoints) ALSO mirror that reasoning into `content` as a
	// <think>…</think> span; once the explicit channel is seen, the inline copy is
	// a duplicate and is dropped so reasoning is not counted twice.
	sawReasoning bool
}

func newStreamState() *streamState {
	return &streamState{textIndex: -1, thinkingIndex: -1, toolIndex: map[int]int{}}
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
	// Only the first choice is consumed: runcode never requests n>1, and folding
	// multiple choices into one block stream would interleave unrelated outputs.
	if len(c.Choices) > 0 {
		choice := c.Choices[0]
		events = append(events, s.delta(choice.Delta)...)
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			s.finish = *choice.FinishReason
		}
	}
	return events
}

func (s *streamState) delta(d chatDelta) []llm.StreamEvent {
	var events []llm.StreamEvent
	// Explicit reasoning channel (reasoning_content / reasoning) → thinking block.
	if r := reasoningText(d); r != "" {
		s.sawReasoning = true
		events = append(events, s.thinkingDelta(r)...)
	}
	// Answer text, splitting any inline <think>…</think> into thinking vs answer.
	if d.Content != "" {
		events = append(events, s.contentDelta(d.Content)...)
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

// textDelta emits an answer-text delta, starting the text block on first use.
func (s *streamState) textDelta(text string) []llm.StreamEvent {
	if text == "" {
		return nil
	}
	var events []llm.StreamEvent
	if s.textIndex < 0 {
		s.textIndex = s.assign()
		events = append(events, llm.StreamEvent{
			Type:  llm.StreamEventTypeContentBlockStart,
			Index: s.textIndex,
			Block: &llm.ContentBlock{Type: llm.ContentBlockTypeText},
		})
	}
	return append(events, llm.StreamEvent{
		Type:  llm.StreamEventTypeContentBlockDelta,
		Index: s.textIndex,
		Delta: &llm.ContentDelta{Text: text},
	})
}

// thinkingDelta emits a reasoning delta into a thinking block. The session never
// streams thinking to the UI and the converter drops it on the way back to the
// provider, so reasoning stays separate from the answer.
func (s *streamState) thinkingDelta(text string) []llm.StreamEvent {
	if text == "" {
		return nil
	}
	var events []llm.StreamEvent
	if s.thinkingIndex < 0 {
		s.thinkingIndex = s.assign()
		events = append(events, llm.StreamEvent{
			Type:  llm.StreamEventTypeContentBlockStart,
			Index: s.thinkingIndex,
			Block: &llm.ContentBlock{Type: llm.ContentBlockTypeThinking},
		})
	}
	return append(events, llm.StreamEvent{
		Type:  llm.StreamEventTypeContentBlockDelta,
		Index: s.thinkingIndex,
		Delta: &llm.ContentDelta{Thinking: text},
	})
}

// contentDelta routes a content fragment to the answer or to thinking, honoring
// inline <think>…</think> tags that may be split across streamed chunks.
func (s *streamState) contentDelta(chunk string) []llm.StreamEvent {
	var events []llm.StreamEvent
	s.pending += chunk
	for s.pending != "" {
		if !s.inThink {
			if i := strings.Index(s.pending, thinkOpen); i >= 0 {
				events = append(events, s.textDelta(s.pending[:i])...)
				s.pending = s.pending[i+len(thinkOpen):]
				s.inThink = true
				continue
			}
			h := holdback(s.pending, thinkOpen)
			events = append(events, s.textDelta(s.pending[:len(s.pending)-h])...)
			s.pending = s.pending[len(s.pending)-h:]
			return events
		}
		if i := strings.Index(s.pending, thinkClose); i >= 0 {
			events = append(events, s.emitInlineThink(s.pending[:i])...)
			s.pending = s.pending[i+len(thinkClose):]
			s.inThink = false
			continue
		}
		h := holdback(s.pending, thinkClose)
		events = append(events, s.emitInlineThink(s.pending[:len(s.pending)-h])...)
		s.pending = s.pending[len(s.pending)-h:]
		return events
	}
	return events
}

// emitInlineThink emits inline <think> content as a thinking delta, unless the
// stream also delivers reasoning on the explicit reasoning_content/reasoning
// channel — in which case the inline copy duplicates it (some gateways send both)
// and is dropped so the reasoning is not doubled.
func (s *streamState) emitInlineThink(text string) []llm.StreamEvent {
	if s.sawReasoning {
		return nil
	}
	return s.thinkingDelta(text)
}

// holdback returns the length of the longest suffix of s that is a proper,
// non-empty prefix of tag — bytes kept buffered in case the next chunk completes
// the tag.
func holdback(s, tag string) int {
	max := len(tag) - 1
	if max > len(s) {
		max = len(s)
	}
	for n := max; n > 0; n-- {
		if s[len(s)-n:] == tag[:n] {
			return n
		}
	}
	return 0
}

// reasoningText extracts reasoning from a delta, tolerating reasoning_content
// (string) and reasoning (string or {content|text}) shapes.
func reasoningText(d chatDelta) string {
	if d.ReasoningContent != "" {
		return d.ReasoningContent
	}
	if len(d.Reasoning) == 0 {
		return ""
	}
	var str string
	if err := json.Unmarshal(d.Reasoning, &str); err == nil {
		return str
	}
	var obj struct {
		Content string `json:"content"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(d.Reasoning, &obj); err == nil {
		if obj.Content != "" {
			return obj.Content
		}
		return obj.Text
	}
	return ""
}

func (s *streamState) assign() int {
	index := s.next
	s.next++
	s.order = append(s.order, index)
	return index
}

// finishEvents closes every started block and emits the terminal message_stop.
func (s *streamState) finishEvents() []llm.StreamEvent {
	events := make([]llm.StreamEvent, 0, len(s.order)+2)
	// Flush content held back for a possibly-split tag: at end of stream it is
	// literal, so emit it in the current mode (answer unless mid-think).
	if s.pending != "" {
		if s.inThink {
			events = append(events, s.emitInlineThink(s.pending)...)
		} else {
			events = append(events, s.textDelta(s.pending)...)
		}
		s.pending = ""
	}
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
	// A response that emitted tool calls is a tool-use turn regardless of the
	// finish_reason string: some compatible endpoints report "stop" (or omit
	// the reason) even when they returned tool_calls. Length truncation still
	// wins, since it signals the output was cut off.
	if hasTools && reason != "length" {
		return llm.StopReasonToolUse
	}
	switch reason {
	case "stop":
		return llm.StopReasonEndTurn
	case "length":
		return llm.StopReasonMaxTokens
	case "tool_calls", "function_call":
		return llm.StopReasonToolUse
	default:
		// content_filter, empty, and any non-standard reason: report a completed
		// turn rather than leak a token the session loop does not understand.
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
