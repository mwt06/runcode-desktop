package localusage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func coverageTestScope() Scope {
	return Scope{ScopeID: strings.Repeat("1", 32), AccountDigest: strings.Repeat("2", 32), BridgeDigest: strings.Repeat("3", 32), TenantID: "tenant-a"}
}

func TestCoverageMetadataUsesScopeStartAndSafeGaps(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	s := openTemp(t, nil)
	scope := coverageTestScope()
	if err := s.Begin(ctx, sampleStart("later", scope, base.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := s.Begin(ctx, sampleStart("earlier", scope, base)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO coverage_gaps(scope_id,start_at_ms,end_at_ms,reason) VALUES(?,?,?,?)`, scope.ScopeID, base.Add(2*time.Hour).UnixMilli(), base.Add(3*time.Hour).UnixMilli(), "unclean_exit"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO coverage_gaps(scope_id,start_at_ms,end_at_ms,reason) VALUES(?,?,?,?)`, scope.ScopeID, base.Add(4*time.Hour).UnixMilli(), base.Add(5*time.Hour).UnixMilli(), "secret-token https://secret.example"); err != nil {
		t.Fatal(err)
	}
	got, err := s.QueryCoverage(ctx, scope, base, base.Add(6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.CollectionStartedAt == nil || !got.CollectionStartedAt.Equal(base) {
		t.Fatalf("collection start=%v, want earliest scope event %v", got.CollectionStartedAt, base)
	}
	if got.CoverageStatus != "gap" || got.StorageStatus != "ok" || len(got.Gaps) != 2 {
		t.Fatalf("coverage=%+v", got)
	}
	if got.Gaps[0].Reason != "unclean_exit" || strings.Contains(got.Gaps[1].Reason, "secret") {
		t.Fatalf("unsafe reasons=%+v", got.Gaps)
	}
	before, err := s.QueryCoverage(ctx, scope, base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil || before.CoverageStatus != "unknown" {
		t.Fatalf("before collection=%+v err=%v", before, err)
	}
	healthy, err := s.QueryCoverage(ctx, scope, base, base.Add(time.Hour))
	if err != nil || healthy.CoverageStatus != "known" || len(healthy.Gaps) != 0 {
		t.Fatalf("healthy collected range=%+v err=%v", healthy, err)
	}
}

func TestCoverageDiagnosticsPersistWithoutStatisticalAttribution(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	s := openTemp(t, nil)
	scope := coverageTestScope()
	for _, at := range []time.Time{base, base.Add(time.Minute)} {
		if err := s.RecordCoverageGap(ctx, CoverageDiagnostic{At: at, AccountDigest: scope.AccountDigest, Reason: "invalid_platform_scope"}); err != nil {
			t.Fatal(err)
		}
	}
	var scopes, events, diagnostics int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM scopes`).Scan(&scopes)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&events)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM diagnostics`).Scan(&diagnostics)
	if scopes != 0 || events != 0 || diagnostics != 1 {
		t.Fatalf("scopes=%d events=%d diagnostics=%d", scopes, events, diagnostics)
	}
	path := s.Path()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.QueryCoverage(ctx, scope, base, base.Add(time.Hour))
	if err != nil || got.CollectionStartedAt != nil || got.CoverageStatus != "gap" || len(got.Gaps) != 1 {
		t.Fatalf("persisted metadata=%+v err=%v", got, err)
	}
	other := scope
	other.AccountDigest = strings.Repeat("4", 32)
	other.ScopeID = strings.Repeat("5", 32)
	got, err = s.QueryCoverage(ctx, other, base, base.Add(time.Hour))
	if err != nil || len(got.Gaps) != 0 || got.CoverageStatus != "unknown" {
		t.Fatalf("other account got foreign gap: %+v err=%v", got, err)
	}
}

func TestCoverageDiagnosticsRespectScopeAndHalfOpenRange(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t, nil)
	base := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	scope := coverageTestScope()
	if err := s.RecordCoverageGap(ctx, CoverageDiagnostic{At: base, ScopeID: scope.ScopeID, AccountDigest: scope.AccountDigest, BridgeDigest: scope.BridgeDigest, Reason: "begin_failed"}); err != nil {
		t.Fatal(err)
	}
	other := scope
	other.ScopeID = strings.Repeat("4", 32)
	other.TenantID = "tenant-b"
	got, err := s.QueryCoverage(ctx, other, base, base.Add(time.Hour))
	if err != nil || len(got.Gaps) != 0 {
		t.Fatalf("other tenant got foreign gap: %+v err=%v", got, err)
	}
	got, err = s.QueryCoverage(ctx, scope, base.Add(-time.Hour), base)
	if err != nil || len(got.Gaps) != 0 {
		t.Fatalf("exclusive end includes diagnostic: %+v err=%v", got, err)
	}
	got, err = s.QueryCoverage(ctx, scope, base, base.Add(time.Hour))
	if err != nil || got.StorageStatus != "degraded" || got.CoverageStatus != "gap" {
		t.Fatalf("failed write marked healthy: %+v err=%v", got, err)
	}
}

func TestCoverageRecorderMarksBeginAndFinishFailures(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t, nil)
	base := time.Now().UTC().Truncate(time.Millisecond)
	scope := coverageTestScope()
	start := sampleStart("record-failure", scope, base)
	rec := WithCoverageDiagnostics(s)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_usage_events BEFORE INSERT ON events BEGIN SELECT RAISE(FAIL, 'private-error-token'); END`); err != nil {
		t.Fatal(err)
	}
	if err := rec.Begin(ctx, start); err == nil {
		t.Fatal("expected real database write failure")
	}
	if err := rec.Finish(ctx, FinishRecord{CallID: start.CallID, Status: StatusSucceeded, FinishedAt: base.Add(time.Second), FrozenStart: start}); err == nil {
		t.Fatal("expected real database write failure")
	}
	got, err := s.QueryCoverage(ctx, scope, base.Add(-time.Second), time.Now().Add(time.Second))
	if err != nil || got.StorageStatus != "degraded" || len(got.Gaps) != 2 {
		t.Fatalf("failures hidden: %+v err=%v", got, err)
	}
	for _, gap := range got.Gaps {
		if strings.Contains(gap.Reason, "private") {
			t.Fatalf("unsafe failure detail: %+v", gap)
		}
	}
}

func TestCoverageDiagnosticsSurviveDiagnosticWriteFailure(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t, nil)
	base := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_diagnostics BEFORE INSERT ON diagnostics BEGIN SELECT RAISE(FAIL, 'private-error-token'); END`); err != nil {
		t.Fatal(err)
	}
	err := s.RecordCoverageGap(ctx, CoverageDiagnostic{At: base, Reason: "missing_stable_identity"})
	if err == nil {
		t.Fatal("expected failed persistence")
	}
	got, err := s.QueryCoverage(ctx, coverageTestScope(), base.Add(-time.Second), base.Add(time.Second))
	if err != nil || got.StorageStatus != "degraded" || got.CoverageStatus != "gap" || len(got.Gaps) != 1 {
		t.Fatalf("in-memory failure disappeared: %+v err=%v", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueryCoverage(ctx, coverageTestScope(), base, base.Add(time.Hour)); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed query err=%v", err)
	}
}

func TestCoverageRejectsSensitiveDiagnosticPayloads(t *testing.T) {
	s := openTemp(t, nil)
	for _, input := range []CoverageDiagnostic{
		{At: time.Now(), Reason: "https://private.example?token=secret"},
		{At: time.Now(), Reason: "missing_stable_identity", AccountDigest: "opaque-token"},
		{At: time.Now(), Reason: "missing_stable_identity", BridgeDigest: "https://private.example"},
	} {
		if err := s.RecordCoverageGap(context.Background(), input); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("unsafe diagnostic accepted: %v", err)
		}
	}
}

func TestCoverageDiagnosticCapacityKeepsConservativeOverflow(t *testing.T) {
	s := openTemp(t, nil)
	base := time.Now().UTC().Truncate(time.Millisecond)
	// Capacity is a persistence invariant, independent of the best-effort 10ms
	// hot-path budget. Exercise its real SQL helper in one fixture transaction;
	// deadline and in-memory fallback behavior have separate controlled tests.
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 1; i <= 270; i++ {
		at := base.Add(time.Duration(i) * time.Millisecond).UnixMilli()
		if _, err := persistCoverageEntry(ctx, tx, coverageEntry{StartMs: at, EndMs: at + 1, AccountDigest: fmt.Sprintf("%032x", i), Reason: "missing_stable_identity"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM diagnostics WHERE code LIKE 'coverage_gap:%'`).Scan(&count); err != nil || count > 256 {
		t.Fatalf("diagnostics are unbounded: count=%d err=%v", count, err)
	}
	scope := coverageTestScope()
	scope.AccountDigest = fmt.Sprintf("%032x", 270)
	got, err := s.QueryCoverage(context.Background(), scope, base, base.Add(time.Second))
	if err != nil || len(got.Gaps) != 1 || got.Gaps[0].Reason != "coverage_history_compacted" {
		t.Fatalf("overflow silently lost coverage: %+v err=%v", got, err)
	}
}

func TestCoverageMemoryCapacityKeepsConservativeOverflow(t *testing.T) {
	s := openTemp(t, nil)
	base := time.Now().UTC().Truncate(time.Millisecond)
	for i := 1; i <= 270; i++ {
		at := base.Add(time.Duration(i) * time.Millisecond).UnixMilli()
		s.coverage.add(coverageEntry{StartMs: at, EndMs: at + 1, AccountDigest: fmt.Sprintf("%032x", i), Reason: "missing_stable_identity"})
	}
	if count := len(s.coverage.snapshot()); count > coverageDiagnosticLimit {
		t.Fatalf("memory diagnostics are unbounded: count=%d", count)
	}
	scope := coverageTestScope()
	scope.AccountDigest = fmt.Sprintf("%032x", 270)
	got, err := s.QueryCoverage(context.Background(), scope, base, base.Add(time.Second))
	if err != nil || got.StorageStatus != "degraded" || len(got.Gaps) != 1 || got.Gaps[0].Reason != "coverage_history_compacted" {
		t.Fatalf("memory overflow silently lost coverage: %+v err=%v", got, err)
	}
}

func TestCoverageQueryHonorsCancellation(t *testing.T) {
	s := openTemp(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.QueryCoverage(ctx, coverageTestScope(), time.Time{}, time.Time{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled query err=%v", err)
	}
}

// A deadline can pass before its timer goroutine delivers cancellation. This
// models the observed parent context (Err=nil with negative remaining time)
// without depending on scheduler timing or Windows timer resolution.
type unsignalledCoverageDeadline struct {
	context.Context
	deadline time.Time
}

func (c unsignalledCoverageDeadline) Deadline() (time.Time, bool) { return c.deadline, true }

func TestCoverageDiagnosticRejectsUnsignalledExpiredDeadline(t *testing.T) {
	s := openTemp(t, nil)
	at := time.Now().UTC().Truncate(time.Millisecond)
	ctx := unsignalledCoverageDeadline{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
	err := s.RecordCoverageGap(ctx, CoverageDiagnostic{At: at, Reason: "begin_failed"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired diagnostic write error=%v", err)
	}
	assertOnlyMemoryCoverage(t, s, coverageTestScope(), at)
}

func TestCoverageRecorderRejectsUnsignalledExpiredDeadline(t *testing.T) {
	for _, phase := range []string{"begin", "finish"} {
		t.Run(phase, func(t *testing.T) {
			s := openTemp(t, nil)
			at := time.Now().UTC().Truncate(time.Millisecond)
			start := sampleStart("expired-write", coverageTestScope(), at)
			ctx := unsignalledCoverageDeadline{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
			rec := WithCoverageDiagnostics(s)
			var err error
			if phase == "begin" {
				err = rec.Begin(ctx, start)
			} else {
				err = rec.Finish(ctx, FinishRecord{CallID: start.CallID, Status: StatusSucceeded, FinishedAt: at, FrozenStart: start})
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expired %s error=%v", phase, err)
			}
			var events int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&events); err != nil || events != 0 {
				t.Fatalf("expired %s wrote %d events, error=%v", phase, events, err)
			}
			assertOnlyMemoryCoverage(t, s, start.Scope, at)
		})
	}
}

func TestCaptureStateRejectsUnsignalledExpiredDeadline(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			s := openTemp(t, nil)
			wantMarkers := 0
			if enabled {
				if err := s.SetCaptureEnabled(context.Background(), false); err != nil {
					t.Fatal(err)
				}
				wantMarkers = 1
			}
			ctx := unsignalledCoverageDeadline{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
			if err := s.SetCaptureEnabled(ctx, enabled); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expired capture state write error=%v", err)
			}
			var markers, diagnostics int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM meta WHERE key=?`, captureDisabledSinceKey).Scan(&markers); err != nil || markers != wantMarkers {
				t.Fatalf("expired capture state changed markers: count=%d error=%v", markers, err)
			}
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM diagnostics`).Scan(&diagnostics); err != nil || diagnostics != 0 {
				t.Fatalf("expired capture state wrote diagnostics: count=%d error=%v", diagnostics, err)
			}
		})
	}
}

func TestCoverageRecorderCancellationDoesNotStartDiagnosticWrite(t *testing.T) {
	for _, phase := range []string{"begin", "finish"} {
		t.Run(phase, func(t *testing.T) {
			s := openTemp(t, nil)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			at := time.Now().UTC().Truncate(time.Millisecond)
			start := sampleStart("cancelled-write", coverageTestScope(), at)
			rec := WithCoverageDiagnostics(s)
			var err error
			if phase == "begin" {
				err = rec.Begin(ctx, start)
			} else {
				err = rec.Finish(ctx, FinishRecord{CallID: start.CallID, Status: StatusSucceeded, FinishedAt: at, FrozenStart: start})
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled write err=%v", err)
			}
			assertOnlyMemoryCoverage(t, s, start.Scope, at)
		})
	}
}

// This adapter releases the actual occupied SQLite connection only after the
// event write times out, so an incorrectly renewed diagnostic budget could
// immediately write to the now-available database. No timing threshold is used.
type releaseBlockedCoverageRecorder struct {
	*Store
	conn *sql.Conn
}

func (r *releaseBlockedCoverageRecorder) Begin(ctx context.Context, start StartRecord) error {
	err := r.Store.Begin(ctx, start)
	_ = r.conn.Close()
	return err
}

func (r *releaseBlockedCoverageRecorder) Finish(ctx context.Context, finish FinishRecord) error {
	err := r.Store.Finish(ctx, finish)
	_ = r.conn.Close()
	return err
}

func TestCoverageRecorderSharesWriteBudgetWithDiagnostics(t *testing.T) {
	for _, phase := range []string{"begin", "finish"} {
		t.Run(phase, func(t *testing.T) {
			s := openTemp(t, nil)
			conn, err := s.db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			at := time.Now().UTC().Truncate(time.Millisecond)
			start := sampleStart("budget-exhausted", coverageTestScope(), at)
			rec := WithCoverageDiagnostics(&releaseBlockedCoverageRecorder{Store: s, conn: conn})
			if phase == "begin" {
				err = rec.Begin(context.Background(), start)
			} else {
				err = rec.Finish(context.Background(), FinishRecord{CallID: start.CallID, Status: StatusSucceeded, FinishedAt: at, FrozenStart: start})
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("blocked write err=%v", err)
			}
			assertOnlyMemoryCoverage(t, s, start.Scope, at)
		})
	}
}

func assertOnlyMemoryCoverage(t *testing.T, s *Store, scope Scope, at time.Time) {
	t.Helper()
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM diagnostics`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("exhausted write budget was renewed for %d persisted diagnostics", count)
	}
	got, err := s.QueryCoverage(context.Background(), scope, at.Add(-time.Second), at.Add(time.Second))
	if err != nil || got.StorageStatus != "degraded" || got.CoverageStatus != "gap" || len(got.Gaps) != 1 {
		t.Fatalf("failed write lost memory gap: %+v err=%v", got, err)
	}
}
