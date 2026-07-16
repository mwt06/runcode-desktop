package toolpath

import (
	"errors"
	"os"

	"github.com/wt68/runcode/engine/tool"
)

var (
	ErrReadRequired = errors.New("read required")
	ErrReadStale    = errors.New("read stale")
	ErrWriteExists  = errors.New("file already exists; read it before overwriting")
)

type ReadState string

const (
	ReadStateFresh   ReadState = "fresh"
	ReadStateMissing ReadState = "missing"
	ReadStatePartial ReadState = "partial"
	ReadStateStale   ReadState = "stale"
)

func FreshReadState(path string, tctx *tool.Context) ReadState {
	if tctx == nil || tctx.ReadSet == nil {
		return ReadStateMissing
	}
	entry, ok := tctx.ReadSet[path]
	if !ok {
		return ReadStateMissing
	}
	if !entry.Complete {
		return ReadStatePartial
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != entry.Size || !info.ModTime().Equal(entry.ModTime) {
		return ReadStateStale
	}
	return ReadStateFresh
}

func RequireFreshRead(path string, tctx *tool.Context) error {
	switch FreshReadState(path, tctx) {
	case ReadStateFresh:
		return nil
	case ReadStateStale:
		return ErrReadStale
	default:
		return ErrReadRequired
	}
}

// RequireOverwritable gates overwriting an existing file. It mirrors
// RequireFreshRead but frames a missing/partial read as ErrWriteExists ("the file
// already exists") instead of ErrReadRequired: for Write the salient fact is that
// an existing file would be clobbered, not that a read is procedurally required.
// A stale read still surfaces as ErrReadStale so the agent re-reads before
// overwriting changed content.
func RequireOverwritable(path string, tctx *tool.Context) error {
	switch FreshReadState(path, tctx) {
	case ReadStateFresh:
		return nil
	case ReadStateStale:
		return ErrReadStale
	default:
		return ErrWriteExists
	}
}
