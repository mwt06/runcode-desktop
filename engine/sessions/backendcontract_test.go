package sessions_test

import (
	"testing"

	"github.com/wt68/runcode/engine/sessions"
	"github.com/wt68/runcode/engine/sessions/backendtest"
)

// The built-in backends are held to the same contract future remote
// implementations must meet: one suite, every implementation.

func harnessFor(kind string) backendtest.Harness {
	return func(t *testing.T) backendtest.Factory {
		dir := t.TempDir()
		return func(t *testing.T) sessions.Backend {
			backend, err := sessions.OpenBackend(dir, kind)
			if err != nil {
				t.Fatalf("OpenBackend(%s): %v", kind, err)
			}
			return backend
		}
	}
}

func TestJSONLBackendContract(t *testing.T) {
	backendtest.Run(t, harnessFor(sessions.BackendJSONL))
}

func TestSQLiteBackendContract(t *testing.T) {
	backendtest.Run(t, harnessFor(sessions.BackendSQLite))
}
