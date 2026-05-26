package telemetry

import (
	"context"
	"io"
)

type Recorder interface {
	Record(ctx context.Context, event Event)
	Close(ctx context.Context) error
}

type noopRecorder struct{}

func Noop() Recorder {
	return noopRecorder{}
}

func (noopRecorder) Record(context.Context, Event) {}

func (noopRecorder) Close(context.Context) error {
	return nil
}

func NewSync(w io.Writer) Recorder {
	return NewJSONL(w)
}
