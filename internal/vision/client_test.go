package vision

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/imageinput"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

type testProvider struct {
	calls    int
	requests []llm.Request
	build    func(int) *testStream
	streams  []*testStream
}

func (*testProvider) Name() string                   { return "fixture" }
func (*testProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (p *testProvider) Stream(_ context.Context, req llm.Request) (llm.Stream, error) {
	p.requests = append(p.requests, req)
	p.calls++
	s := p.build(p.calls)
	p.streams = append(p.streams, s)
	return s, nil
}

type testStream struct {
	events chan llm.StreamEvent
	closed bool
}

func (s *testStream) Events() <-chan llm.StreamEvent { return s.events }
func (*testStream) Err() error                       { return nil }
func (s *testStream) Close() error                   { s.closed = true; return nil }
func events(es ...llm.StreamEvent) *testStream {
	c := make(chan llm.StreamEvent, len(es))
	for _, e := range es {
		c <- e
	}
	close(c)
	return &testStream{events: c}
}
func startUsage() llm.StreamEvent { return llm.StreamEvent{Usage: &llm.Usage{InputTokens: 11}} }
func answer(text string) llm.StreamEvent {
	return llm.StreamEvent{Delta: &llm.ContentDelta{Text: text, Thinking: "observed, not assumed"}}
}
func stop(reason llm.StopReason) llm.StreamEvent {
	return llm.StreamEvent{Type: llm.StreamEventTypeMessageStop, StopReason: reason, Usage: &llm.Usage{OutputTokens: 7}}
}

func TestClientBoundedToolFreeInference(t *testing.T) {
	p := &testProvider{build: func(n int) *testStream {
		if n == 1 {
			return events(startUsage(), answer(""), stop(llm.StopReasonEndTurn))
		}
		return events(startUsage(), answer("chart 42"), stop(llm.StopReasonEndTurn))
	}}
	q := imageinput.Query{Question: "read chart", Images: []imageinput.Image{{Ref: "session-image", Source: llm.ImageSource{MediaType: "image/png", Data: []byte("fixture")}}}}
	a, err := (Client{Provider: p, Model: "vision"}).Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 || a.Usage.InputTokens != 22 || a.Usage.OutputTokens != 14 || !strings.Contains(a.Thinking, "observed") {
		t.Fatalf("answer=%+v calls=%d", a, p.calls)
	}
	for _, req := range p.requests {
		if req.Model != "vision" || len(req.Tools) != 0 || len(req.Messages) != 1 || len(req.Messages[0].Content) != 3 {
			t.Fatalf("request=%+v", req)
		}
	}
	for _, s := range p.streams {
		if !s.closed {
			t.Fatal("stream not closed")
		}
	}
}
func TestClientRejectsPartialToolsAndTruncation(t *testing.T) {
	tests := []struct {
		name  string
		build func(int) *testStream
		calls int
		want  error
	}{
		{"partial", func(int) *testStream { return events(startUsage(), answer("partial")) }, 1, io.ErrUnexpectedEOF},
		{"tools", func(int) *testStream {
			return events(llm.StreamEvent{Block: &llm.ContentBlock{Type: llm.ContentBlockTypeToolUse}})
		}, 1, nil},
		{"truncated", func(int) *testStream { return events(startUsage(), answer("partial"), stop(llm.StopReasonMaxTokens)) }, 2, nil},
		{"thinking-only", func(int) *testStream { return events(startUsage(), answer(""), stop(llm.StopReasonEndTurn)) }, 2, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &testProvider{build: tt.build}
			a, err := (Client{Provider: p}).Analyze(context.Background(), imageinput.Query{})
			if err == nil || p.calls != tt.calls {
				t.Fatalf("calls=%d answer=%+v err=%v", p.calls, a, err)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatal(err)
			}
			if tt.name != "tools" && a.Usage.InputTokens != 11*tt.calls {
				t.Fatal("lost paid usage")
			}
		})
	}
}
func TestClientCancellationAndGate(t *testing.T) {
	p := &testProvider{build: func(int) *testStream { return &testStream{events: make(chan llm.StreamEvent)} }}
	_, err := (Client{Provider: p, Timeout: 20 * time.Millisecond}).Analyze(context.Background(), imageinput.Query{})
	if !errors.Is(err, context.DeadlineExceeded) || !p.streams[0].closed {
		t.Fatalf("err=%v", err)
	}
	_, err = (Client{Provider: p, Check: func(context.Context) error { return errors.New("OA lock") }}).Analyze(context.Background(), imageinput.Query{})
	if err == nil || p.calls != 1 {
		t.Fatal("privacy gate bypassed")
	}
}
