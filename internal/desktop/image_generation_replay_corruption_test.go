package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeCorruptReplayLedger(t *testing.T, workspace, sessionID string) {
	t.Helper()
	dir, err := imageGenerationLedgerDir(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, dir, "gen_corrupt.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestImageGenerationResumeIsolatesCorruptLedger(t *testing.T) {
	for _, mode := range []string{"authoritative state", "ledger only"} {
		t.Run(mode, func(t *testing.T) {
			f, record := completedImageReplaySession(t)
			if err := f.app.CloseSession(f.id); err != nil {
				t.Fatal(err)
			}
			record.AccountingStatus = "verified"
			status := "succeeded"
			if mode == "authoritative state" {
				// The complete persisted tool JSON still says success, while the
				// valid ledger conservatively has no confirmed terminal outcome.
				record.Status, record.Result, status = "submitted", nil, "unknown"
			} else {
				rewriteImageReplayHistory(t, f.cwd, f.id, "ledger only")
			}
			if err := writeImageGenerationLedger(f.cwd, record); err != nil {
				t.Fatal(err)
			}
			writeCorruptReplayLedger(t, f.cwd, f.id)
			if _, err := listImageGenerationLedgers(f.cwd, f.id); err == nil {
				t.Fatal("strict ledger loader accepted corruption")
			}
			calls := f.hostCalls.Load()
			for attempt := 0; attempt < 2; attempt++ {
				resumed, err := f.app.ResumeSession(f.id)
				if err != nil {
					t.Fatal(err)
				}
				result := assertReplayedImage(t, f, resumed, record.QuestionID, status, "verified")
				if result.GenerationID != record.GenerationID {
					t.Fatalf("replay changed generation identity: %+v", result)
				}
				if err := f.app.CloseSession(f.id); err != nil {
					t.Fatal(err)
				}
			}
			if f.bridge.posts.Load() != 1 || f.hostCalls.Load() != calls {
				t.Fatalf("partial replay executed generation: posts=%d hostCalls=%d", f.bridge.posts.Load(), f.hostCalls.Load())
			}
			if err := reserveImageGenerationTurn(f.cwd, f.id, record.TurnID, "gen_duplicate"); !errors.Is(err, errImageGenerationTurnSpent) {
				t.Fatalf("partial replay renewed consumed budget: %v", err)
			}
		})
	}
}
