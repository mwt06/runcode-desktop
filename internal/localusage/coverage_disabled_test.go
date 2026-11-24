package localusage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCaptureDisabledIntervalsKeepHalfOpenBoundaries(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	now := base
	store := openTemp(t, func() time.Time { return now })
	scope := coverageTestScope()
	start := sampleStart("before-disabled", scope, base.Add(-time.Hour))
	if err := store.Begin(ctx, start); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(ctx, FinishRecord{CallID: start.CallID, Status: StatusSucceeded, FinishedAt: base.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCaptureEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	now = base.Add(30 * time.Minute)
	active, err := store.QueryCoverage(ctx, scope, base, base.Add(time.Hour))
	if err != nil || len(active.Gaps) != 1 || !active.Gaps[0].StartAt.Equal(base) || active.Gaps[0].Reason != "capture_disabled" {
		t.Fatalf("active disabled coverage=%+v error=%v", active, err)
	}
	path := store.Path()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	now = base.Add(time.Hour)
	store, err = Open(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	// An additional disabled startup must not lose the original start time.
	if err := store.SetCaptureEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	now = base.Add(2 * time.Hour)
	if err := store.SetCaptureEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCaptureEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		start, end time.Time
		gaps       int
	}{
		{"exclusive_before", base.Add(-time.Minute), base, 0},
		{"inclusive_start", base, base.Add(time.Minute), 1},
		{"clipped_middle", base.Add(time.Minute), base.Add(2 * time.Minute), 1},
		{"exclusive_end", now, now.Add(time.Minute), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coverage, err := store.QueryCoverage(ctx, scope, tc.start, tc.end)
			if err != nil || len(coverage.Gaps) != tc.gaps {
				t.Fatalf("coverage=%+v error=%v", coverage, err)
			}
			if tc.gaps == 1 && (!coverage.Gaps[0].StartAt.Equal(tc.start) || !coverage.Gaps[0].EndAt.Equal(tc.end)) {
				t.Fatalf("gap was not clipped: %+v", coverage.Gaps)
			}
		})
	}
	// Another disabled interval must leave the enabled hour between them healthy.
	now = base.Add(3 * time.Hour)
	if err := store.SetCaptureEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	now = base.Add(4 * time.Hour)
	if err := store.SetCaptureEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	coverage, err := store.QueryCoverage(ctx, scope, base.Add(2*time.Hour), base.Add(3*time.Hour))
	if err != nil || coverage.CoverageStatus != "known" || len(coverage.Gaps) != 0 {
		t.Fatalf("enabled interval lost coverage=%+v error=%v", coverage, err)
	}
	// The disabled process had no frozen identity; changing account, Bridge and
	// tenant must not turn its unattributed interval into a known zero.
	other := Scope{ScopeID: strings.Repeat("4", 32), AccountDigest: strings.Repeat("5", 32), BridgeDigest: strings.Repeat("6", 32), TenantID: "tenant-b"}
	coverage, err = store.QueryCoverage(ctx, other, base, now)
	if err != nil || coverage.CoverageStatus != "gap" || len(coverage.Gaps) != 2 || coverage.CollectionStartedAt != nil {
		t.Fatalf("unknown disabled identity=%+v error=%v", coverage, err)
	}
	var scopes int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM scopes`).Scan(&scopes); err != nil || scopes != 1 {
		t.Fatalf("disabled state invented scopes=%d error=%v", scopes, err)
	}
}

func TestCaptureDisabledIntervalSurvivesProcessExit(t *testing.T) {
	const fixturePathEnv = "RUNCODE_TEST_CAPTURE_DISABLED_PATH"
	base := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	if path := os.Getenv(fixturePathEnv); path != "" {
		store, err := Open(path, func() time.Time { return base })
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetCaptureEnabled(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		// Simulate process death: skip Store.Close and all deferred cleanup.
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "usage.sqlite3")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestCaptureDisabledIntervalSurvivesProcessExit$")
	cmd.Env = append(os.Environ(), fixturePathEnv+"="+path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("disabled process failed: %v\n%s", err, output)
	}
	resumed := base.Add(time.Hour)
	store, err := Open(path, func() time.Time { return resumed })
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetCaptureEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	coverage, err := store.QueryCoverage(context.Background(), coverageTestScope(), base, resumed)
	if err != nil || len(coverage.Gaps) != 1 || coverage.Gaps[0].Reason != "capture_disabled" || !coverage.Gaps[0].StartAt.Equal(base) || !coverage.Gaps[0].EndAt.Equal(resumed) {
		t.Fatalf("disabled process history=%+v error=%v", coverage, err)
	}
}

func TestCaptureDisabledIntervalsHaveBoundedHistory(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	now := base
	store := openTemp(t, func() time.Time { return now })
	for i := 0; i < coverageDiagnosticLimit+10; i++ {
		now = base.Add(time.Duration(i*2) * time.Second)
		if err := store.SetCaptureEnabled(ctx, false); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
		if err := store.SetCaptureEnabled(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM diagnostics WHERE code LIKE 'coverage_gap:%'`).Scan(&count); err != nil || count > coverageDiagnosticLimit {
		t.Fatalf("disabled history is unbounded: count=%d error=%v", count, err)
	}
	coverage, err := store.QueryCoverage(ctx, coverageTestScope(), now.Add(-time.Second), now)
	if err != nil || len(coverage.Gaps) != 1 || coverage.Gaps[0].Reason != "coverage_history_compacted" {
		t.Fatalf("compacted disabled history disappeared: %+v error=%v", coverage, err)
	}
}

func TestCaptureDisabledIntervalClosureIsAtomic(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	now := base
	store := openTemp(t, func() time.Time { return now })
	if err := store.SetCaptureEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_disabled_history BEFORE INSERT ON diagnostics BEGIN SELECT RAISE(FAIL, 'write unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	now = base.Add(time.Hour)
	if err := store.SetCaptureEnabled(ctx, true); err == nil {
		t.Fatal("expected failed interval closure")
	}
	coverage, err := store.QueryCoverage(ctx, coverageTestScope(), base, now)
	if err != nil || len(coverage.Gaps) != 1 || coverage.Gaps[0].Reason != "capture_disabled" {
		t.Fatalf("failed closure erased the active marker: %+v error=%v", coverage, err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_disabled_history`); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCaptureEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	coverage, err = store.QueryCoverage(ctx, coverageTestScope(), base, now)
	if err != nil || len(coverage.Gaps) != 1 {
		t.Fatalf("retried closure=%+v error=%v", coverage, err)
	}
}
