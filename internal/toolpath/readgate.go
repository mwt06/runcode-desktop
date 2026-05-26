package toolpath

import (
	"errors"
	"os"

	"github.com/wt68/runcode/pkg/tool"
)

var (
	ErrReadRequired = errors.New("read required")
	ErrReadStale    = errors.New("read stale")
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
