package openai

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// SSEDumpDirEnv names the environment variable that enables raw SSE capture.
// When it points at a directory, each streaming response is mirrored verbatim to
// a file there (one file per request), so the exact bytes the provider sent can
// be inspected offline — e.g. to confirm whether duplicated reasoning tokens
// originate upstream or in our decoding. Unset (the default) disables capture
// entirely with zero overhead.
const SSEDumpDirEnv = "RUNCODE_SSE_DUMP_DIR"

var sseDumpSeq uint64

// tapSSE mirrors the raw SSE byte stream to a timestamped file under the capture
// directory, returning a body that writes through to it. Capture is best-effort:
// any setup error returns the original body unchanged, so dumping can never break
// a real request.
func tapSSE(body io.ReadCloser) io.ReadCloser {
	dir := strings.TrimSpace(os.Getenv(SSEDumpDirEnv))
	if dir == "" {
		return body
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return body
	}
	name := fmt.Sprintf("sse-%d-%d.txt", time.Now().UnixNano(), atomic.AddUint64(&sseDumpSeq, 1))
	file, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return body
	}
	return &tappedBody{body: body, file: file}
}

// tappedBody is an io.ReadCloser that copies every byte read from the underlying
// body into a capture file. A write error to the capture file is ignored so it
// never disturbs the stream being read.
type tappedBody struct {
	body io.ReadCloser
	file *os.File
}

func (t *tappedBody) Read(p []byte) (int, error) {
	n, err := t.body.Read(p)
	if n > 0 {
		_, _ = t.file.Write(p[:n])
	}
	return n, err
}

func (t *tappedBody) Close() error {
	_ = t.file.Close()
	return t.body.Close()
}
