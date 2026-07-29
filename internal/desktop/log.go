package desktop

// A best-effort diagnostic log. The packaged desktop app is a frameless
// production Wails build with no console, so a turn that fails or produces
// nothing leaves no visible trail. This writes the turn lifecycle (submission,
// end/error/queued, retries, warnings) to %AppData%/runcode/desktop.log so such
// problems can be diagnosed after the fact. It never affects the app: any logging
// failure is swallowed, and high-frequency streaming events are skipped so the
// file stays readable.

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

// logMu serializes writes so concurrent turns don't interleave partial lines. The
// file is opened and closed per line rather than held open: turn-lifecycle logging
// is low frequency, and not holding a handle keeps the file deletable (and avoids a
// lingering lock on Windows, which also let test temp dirs be cleaned up).
var logMu sync.Mutex

// desktopLogPath is the diagnostic log's location, beside desktop.json.
func desktopLogPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runcode", "desktop.log"), nil
}

// debugLog appends one timestamped line to the diagnostic log. Best-effort: any
// failure (no config dir, unwritable path) is swallowed and never surfaces.
func debugLog(format string, args ...any) {
	logMu.Lock()
	defer logMu.Unlock()

	path, err := desktopLogPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	// Keep the log bounded: start fresh if a prior run left it large.
	if info, statErr := os.Stat(path); statErr == nil && info.Size() > 4<<20 {
		_ = os.Remove(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "%s  %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, args...))
}

// logEnvelope records the turn-lifecycle events that matter for diagnosing a turn
// that fails or produces nothing. Submission is logged at the call site; here we
// capture its outcome (end/error/queued), retries, and warnings. The high-frequency
// streaming events (assistant deltas, tool progress) are intentionally skipped.
func logEnvelope(env protocol.Envelope) {
	switch env.Event {
	case protocol.EventTurnError:
		if te, ok := env.Payload.(protocol.TurnError); ok {
			debugLog("turn:error seq=%d: %s", env.Seq, te.Error)
			return
		}
		debugLog("turn:error seq=%d: %+v", env.Seq, env.Payload)
	case protocol.EventTurnEnd:
		debugLog("turn:end seq=%d: %+v", env.Seq, env.Payload)
	case protocol.EventTurnQueued:
		debugLog("turn:queued seq=%d", env.Seq)
	case protocol.EventRetry:
		debugLog("llm:retry seq=%d: %+v", env.Seq, env.Payload)
	case protocol.EventWarning:
		debugLog("warning seq=%d: %+v", env.Seq, env.Payload)
	}
}
