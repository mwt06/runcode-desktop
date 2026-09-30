package desktop

import (
	"context"
	"os"

	"github.com/wt68/runcode/internal/download"
)

// fetchToFile is the shared download adapter used by desktop feature callers.
func fetchToFile(ctx context.Context, rawURL, label string, w *os.File, maxBytes int64, progress func(int64, int64)) (string, int64, error) {
	return download.ToFile(ctx, rawURL, label, w, maxBytes, progress)
}
