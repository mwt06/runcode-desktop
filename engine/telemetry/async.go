package telemetry

import (
	"context"
	"sync"
	"sync/atomic"
)

const defaultAsyncBufferSize = 256

type AsyncOptions struct {
	BufferSize int
}

type AsyncRecorder struct {
	sink    Recorder
	events  chan Event
	done    chan struct{}
	once    sync.Once
	dropped atomic.Uint64
}

func NewAsync(sink Recorder, opts AsyncOptions) *AsyncRecorder {
	if sink == nil {
		sink = Noop()
	}
	bufferSize := opts.BufferSize
	if bufferSize <= 0 {
		bufferSize = defaultAsyncBufferSize
	}
	recorder := &AsyncRecorder{
		sink:   sink,
		events: make(chan Event, bufferSize),
		done:   make(chan struct{}),
	}
	go recorder.run()
	return recorder
}

func (r *AsyncRecorder) Record(_ context.Context, event Event) {
	if r == nil {
		return
	}
	select {
	case r.events <- event:
	default:
		r.dropped.Add(1)
	}
}

func (r *AsyncRecorder) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.once.Do(func() {
		close(r.events)
	})
	select {
	case <-r.done:
		return r.sink.Close(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *AsyncRecorder) Dropped() uint64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}

func (r *AsyncRecorder) run() {
	defer close(r.done)
	for event := range r.events {
		r.sink.Record(context.Background(), event)
	}
}
