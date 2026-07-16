package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
)

type JSONLRecorder struct {
	w       io.Writer
	mu      sync.Mutex
	dropped atomic.Uint64
}

func NewJSONL(w io.Writer) *JSONLRecorder {
	return &JSONLRecorder{w: w}
}

func (r *JSONLRecorder) Record(_ context.Context, event Event) {
	if r == nil || r.w == nil {
		return
	}
	data, err := json.Marshal(event)
	if err != nil {
		r.dropped.Add(1)
		return
	}
	data = append(data, '\n')
	r.mu.Lock()
	_, err = r.w.Write(data)
	r.mu.Unlock()
	if err != nil {
		r.dropped.Add(1)
	}
}

func (r *JSONLRecorder) Close(context.Context) error {
	return nil
}

func (r *JSONLRecorder) Dropped() uint64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}
