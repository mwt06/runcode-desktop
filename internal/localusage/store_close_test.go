package localusage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreClosePreservesPendingRecovery(t *testing.T) {
	base := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	now := testClock(t, base)
	path := filepath.Join(t.TempDir(), "usage.sqlite3")
	store, err := Open(path, now)
	if err != nil {
		t.Fatal(err)
	}
	start := sampleStart("unfinished-at-close", sampleScope("close-recovery"), base.Add(-time.Minute))
	if err := store.Begin(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, now)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	status, err := reopened.CallStatus(context.Background(), start.CallID)
	if err != nil || status != StatusUnknown {
		t.Fatalf("unfinished close lost crash recovery: status=%q err=%v", status, err)
	}
	coverage, err := reopened.QueryCoverage(context.Background(), start.Scope, start.StartedAt, base.Add(time.Minute))
	if err != nil || len(coverage.Gaps) == 0 {
		t.Fatalf("unfinished close claimed complete coverage: %+v err=%v", coverage, err)
	}
}

func TestStoreOpenRecoversLegacyPendingWithoutDirtyMarker(t *testing.T) {
	base := time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC)
	now := testClock(t, base)
	path := filepath.Join(t.TempDir(), "usage.sqlite3")
	store, err := Open(path, now)
	if err != nil {
		t.Fatal(err)
	}
	start := sampleStart("legacy-clean-pending", sampleScope("legacy-clean"), base.Add(-time.Minute))
	if err := store.Begin(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	// Older clients removed this marker even when Close raced with Finish.
	if _, err := store.db.Exec(`DELETE FROM meta WHERE key = 'active_run_id'`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, now)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if status, err := reopened.CallStatus(context.Background(), start.CallID); err != nil || status != StatusUnknown {
		t.Fatalf("legacy pending record stayed invisible: status=%q err=%v", status, err)
	}
	page, err := reopened.QueryPage(context.Background(), QueryFilter{
		ScopeID: start.Scope.ScopeID, StartAt: start.StartedAt, EndAt: base.Add(time.Minute),
	}, "", 20)
	if err != nil || page.TotalCount != 1 || len(page.Calls) != 1 || page.Calls[0].Status != StatusUnknown {
		t.Fatalf("legacy recovery was not queryable: page=%+v err=%v", page, err)
	}
}
