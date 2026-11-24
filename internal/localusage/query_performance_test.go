package localusage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAggregateSQLUsesMaterializedCacheEstimates(t *testing.T) {
	sql := aggregateColumnsSQL()
	if strings.Contains(sql, "FROM events ps") || strings.Contains(sql, "prevTokenFieldSQL") {
		t.Fatalf("aggregate SQL regressed to per-row event subqueries: %s", sql)
	}
	if !strings.Contains(sql, "ce.cache_hit_tokens") || !strings.Contains(sql, "ce.cache_miss_tokens") {
		t.Fatalf("aggregate SQL does not read materialized cache: %s", sql)
	}
}

// A pending start must not consume a user-visible page or its total count.
func TestQueryPageExcludesPendingAndKeepsSnapshot(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	s := openTemp(t, time.Now)
	scope := sampleScope("page-pending")
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		start := sampleStart(id, scope, now.Add(-time.Hour))
		if err := s.Begin(ctx, start); err != nil {
			t.Fatal(err)
		}
		if id != "c" {
			if err := s.Finish(ctx, FinishRecord{CallID: id, Status: StatusSucceeded, FinishedAt: now}); err != nil {
				t.Fatal(err)
			}
		}
	}
	filter := QueryFilter{ScopeID: scope.ScopeID, StartAt: now.Add(-2 * time.Hour), EndAt: now}
	first, err := s.QueryPage(ctx, filter, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.TotalCount != 2 || len(first.Calls) != 1 || first.Calls[0].CallID != "b" || !first.HasMore {
		t.Fatalf("first page included pending or wrong order: %+v", first)
	}
	if err := s.Finish(ctx, FinishRecord{CallID: "c", Status: StatusSucceeded, FinishedAt: now}); err != nil {
		t.Fatal(err)
	}
	second, err := s.QueryPage(ctx, filter, first.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if second.Seq != first.Seq || second.TotalCount != 2 || len(second.Calls) != 1 || second.Calls[0].CallID != "a" || second.HasMore {
		t.Fatalf("late finish changed fixed snapshot: %+v", second)
	}
}

// Holding the write connection must not prevent a bounded read from completing.
func TestQueryDoesNotWaitForWriterConnection(t *testing.T) {
	s := openTemp(t, time.Now)
	now := time.Now()
	if err := s.WaitMaintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = s.QueryPage(ctx, QueryFilter{ScopeID: "missing", StartAt: now.Add(-time.Hour), EndAt: now}, "", 20)
	if err != nil {
		t.Fatalf("read waited for writer connection: %v", err)
	}
}

// Unknown sides must not erase known token values, and pending calls must not
// enter request totals or dimensions. Buckets use half-open boundaries.
func TestSQLAggregatesPartialTokensBucketsAndDimensions(t *testing.T) {
	s := openTemp(t, time.Now)
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	scope := sampleScope("aggregate")
	ctx := context.Background()
	fixtures := []struct {
		id, model, kind, status            string
		offset                             time.Duration
		input, output, hit, miss, duration *int64
	}{
		{"a", "alpha", CallKindLLM, StatusSucceeded, 0, ptrInt64(10), nil, nil, nil, ptrInt64(20)},
		{"b", "alpha", CallKindVision, StatusFailed, time.Hour, ptrInt64(5), ptrInt64(3), ptrInt64(2), ptrInt64(3), ptrInt64(40)},
		{"c", "beta", CallKindLLM, StatusUnknown, 2 * time.Hour, nil, ptrInt64(7), ptrInt64(100), nil, nil},
		{"d", "gamma", CallKindImage, StatusSaveFailed, 3 * time.Hour, nil, nil, nil, nil, nil},
		{"e", "pending-only", CallKindLLM, StatusPending, 0, nil, nil, nil, nil, nil},
		{"f", "beta", CallKindLLM, StatusCancelled, 3 * time.Hour, nil, nil, nil, nil, nil},
	}
	for _, f := range fixtures {
		start := sampleStart(f.id, scope, base.Add(f.offset))
		start.Model, start.CallKind = f.model, f.kind
		if err := s.Begin(ctx, start); err != nil {
			t.Fatal(err)
		}
		if f.status == StatusPending {
			continue
		}
		if err := s.Finish(ctx, FinishRecord{CallID: f.id, Status: f.status, FinishedAt: base.Add(4 * time.Hour), InputTokens: f.input, InputTokensKnown: f.input != nil, OutputTokens: f.output, OutputTokensKnown: f.output != nil, CacheHitTokens: f.hit, CacheHitTokensKnown: f.hit != nil, CacheMissTokens: f.miss, CacheMissTokensKnown: f.miss != nil, DurationMs: f.duration}); err != nil {
			t.Fatal(err)
		}
	}
	f := QueryFilter{ScopeID: scope.ScopeID, StartAt: base, EndAt: base.Add(4 * time.Hour)}
	boundaries := []time.Time{base, base.Add(time.Hour), base.Add(2 * time.Hour), base.Add(3 * time.Hour), base.Add(4 * time.Hour)}
	o, err := s.QueryOverview(ctx, f, boundaries)
	if err != nil {
		t.Fatal(err)
	}
	want := AggregateTotals{RequestCount: 5, TokenCallCount: 4, InputSampleCount: 2, OutputSampleCount: 2, TokenSampleCount: 3, CompleteTokenSampleCount: 1, InputTokens: 15, OutputTokens: 10, TotalTokens: 25, CacheSampleCount: 1, CacheHitTokens: 2, CacheMissTokens: 3, CacheEligibleInputTokens: 5, DurationSumMs: 60, DurationSampleCount: 2, Statuses: StatusTotals{Pending: 1, Succeeded: 1, Failed: 1, Cancelled: 1, Unknown: 1, SaveFailed: 1}}
	if o.Totals != want {
		t.Fatalf("totals=%+v want=%+v", o.Totals, want)
	}
	if len(o.Buckets) != 4 || o.Buckets[0].Totals.RequestCount != 1 || o.Buckets[0].Totals.TotalTokens != 10 || o.Buckets[0].Totals.Statuses.Pending != 1 || o.Buckets[1].Totals.TotalTokens != 8 || o.Buckets[2].Totals.TotalTokens != 7 || o.Buckets[3].Totals.RequestCount != 2 {
		t.Fatalf("buckets=%+v", o.Buckets)
	}
	f.SnapshotSeq = o.Seq
	f.SnapshotPinned = true
	d, err := s.QueryDimensions(ctx, f, "", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if d.Seq != o.Seq || d.TotalCount != 3 || !d.HasMore || len(d.Groups) != 1 || d.Groups[0].Model != "alpha" || d.Groups[0].Totals.TotalTokens != 18 {
		t.Fatalf("dimensions=%+v", d)
	}
	d, err = s.QueryDimensions(ctx, f, "BETA", 1, 10)
	if err != nil || d.TotalCount != 1 || len(d.Groups) != 1 || d.Groups[0].Model != "beta" || d.HasMore {
		t.Fatalf("search=%+v err=%v", d, err)
	}
	plain, err := s.QueryOverview(ctx, f, nil)
	if err != nil || plain.Totals != want {
		t.Fatalf("unbucketed=%+v err=%v", plain, err)
	}
}

func TestSQLAggregatesBoundBucketsAndCancellation(t *testing.T) {
	s := openTemp(t, time.Now)
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	f := QueryFilter{ScopeID: "empty", StartAt: base, EndAt: base.Add(601 * time.Hour)}
	boundaries := make([]time.Time, 602)
	for i := range boundaries {
		boundaries[i] = base.Add(time.Duration(i) * time.Hour)
	}
	if _, err := s.QueryOverview(context.Background(), f, boundaries); err == nil {
		t.Fatal("accepted more than 600 buckets")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.QueryOverview(ctx, f, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query=%v", err)
	}
	if _, err := s.QueryDimensions(ctx, f, "", 1, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled dimensions=%v", err)
	}
}

func TestPinnedEmptySnapshotAndInvalidCursorErrors(t *testing.T) {
	s := openTemp(t, time.Now)
	now := time.Now()
	f := QueryFilter{ScopeID: sampleScope("pinned-zero").ScopeID, StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour)}
	empty, err := s.QueryPage(context.Background(), f, "", 10)
	if err != nil || empty.Seq != 0 {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
	start := sampleStart("late", sampleScope("pinned-zero"), now)
	if err := s.Finish(context.Background(), FinishRecord{CallID: start.CallID, FrozenStart: start, Status: StatusSucceeded, FinishedAt: now}); err != nil {
		t.Fatal(err)
	}
	f.SnapshotPinned = true
	stillEmpty, err := s.QueryPage(context.Background(), f, "", 10)
	if err != nil || stillEmpty.Seq != 0 || stillEmpty.TotalCount != 0 || len(stillEmpty.Calls) != 0 {
		t.Fatalf("pinned empty snapshot changed: %+v %v", stillEmpty, err)
	}
	if _, err := s.QueryPage(context.Background(), f, "garbage", 10); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("invalid cursor=%v", err)
	}
	f.SnapshotSeq = 999
	if _, err := s.QueryPage(context.Background(), f, "", 10); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("invalid snapshot=%v", err)
	}
}

func TestHistoricalMissingFinishStatusRemainsUnknown(t *testing.T) {
	s := openTemp(t, time.Now)
	base := time.Now()
	scope := sampleScope("legacy-status")
	for i, status := range []any{nil, "", "invalid", "pending"} {
		start := sampleStart(fmt.Sprintf("legacy-%d", i), scope, base)
		if err := s.Finish(context.Background(), FinishRecord{CallID: start.CallID, FrozenStart: start, Status: StatusSucceeded, FinishedAt: base}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE events SET status=? WHERE call_id=? AND phase='finish'`, status, start.CallID); err != nil {
			t.Fatal(err)
		}
	}
	f := QueryFilter{ScopeID: scope.ScopeID, StartAt: base.Add(-time.Hour), EndAt: base.Add(time.Hour)}
	page, err := s.QueryPage(context.Background(), f, "", 10)
	if err != nil || page.TotalCount != 4 || len(page.Calls) != 4 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	for _, call := range page.Calls {
		if call.Status != StatusUnknown {
			t.Errorf("historical finish %s has status %q, want unknown", call.CallID, call.Status)
		}
	}
	o, err := s.QueryOverview(context.Background(), f, nil)
	if err != nil || o.Totals.RequestCount != 4 || o.Totals.Statuses.Unknown != 4 || o.Totals.Statuses.Pending != 0 {
		t.Fatalf("overview=%+v err=%v", o, err)
	}
}

func TestReadPoolSaturationDoesNotBlockWritesAndCancels(t *testing.T) {
	s := openTemp(t, time.Now)
	var readers []*sql.Tx
	for i := 0; i < readPoolSize; i++ {
		tx, err := s.readDB.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		readers = append(readers, tx)
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, tx := range readers {
			tx.Rollback()
		}
	}()
	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := s.QueryPage(ctx, QueryFilter{ScopeID: "empty", StartAt: now.Add(-time.Hour), EndAt: now}, "", 10)
		result <- err
	}()
	start := sampleStart("write-with-readers", sampleScope("readers"), now)
	for _, write := range []func() error{
		func() error { return s.Begin(context.Background(), start) },
		func() error {
			return s.Finish(context.Background(), FinishRecord{CallID: start.CallID, Status: StatusSucceeded, FinishedAt: now})
		},
	} {
		before := time.Now()
		err := write()
		elapsed := time.Since(before)
		if err != nil || elapsed >= writeBudget {
			t.Fatalf("writer blocked by readers: %v in %v", err, elapsed)
		}
	}
	if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting query was not canceled: %v", err)
	}
}

func TestWriteConnectionWaitUsesCallerBudget(t *testing.T) {
	s := openTemp(t, time.Now)
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	before := time.Now()
	err = s.Begin(ctx, sampleStart("blocked", sampleScope("budget"), time.Now()))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(before) >= writeBudget {
		t.Fatalf("connection wait exceeded budget: %v in %v", err, time.Since(before))
	}
}

func TestCloseWithConcurrentQueriesReturnsWholeResults(t *testing.T) {
	s := openTemp(t, time.Now)
	f := seedPerformanceCalls(t, s, 500)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			page, err := s.QueryPage(context.Background(), f, "", 20)
			if err != nil && !errors.Is(err, ErrStoreClosed) {
				t.Errorf("query during close: %v", err)
			}
			if err == nil && (len(page.Calls) != 20 || page.TotalCount != 500) {
				t.Errorf("partial page during close: %+v", page)
			}
		}()
	}
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			if err := s.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
		}()
	}
	wg.Wait()
}

func seedPerformanceCalls(tb testing.TB, s *Store, count int) QueryFilter {
	tb.Helper()
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	tx, err := s.db.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	defer tx.Rollback()
	if err := ensureScope(context.Background(), tx, sampleScope("perf"), base.UnixMilli()); err != nil {
		tb.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO events(call_id,phase,run_id,scope_id,account_digest,bridge_digest,tenant_id,model,call_kind,started_at_ms,finished_at_ms,status,input_tokens,input_tokens_known,output_tokens,output_tokens_known,recorded_at_ms) VALUES(?,?,'perf','scope-a-perf','acct-a','bridge-a','wjtest',?,'llm',?,?,?,?,1,2,1,?)`)
	if err != nil {
		tb.Fatal(err)
	}
	defer stmt.Close()
	for i := 0; i < count; i++ {
		at := base.Add(time.Duration(i) * time.Second).UnixMilli()
		id, model := fmt.Sprintf("perf-%06d", i), fmt.Sprintf("model-%03d", i%100)
		if _, err := stmt.Exec(id, "start", model, at, nil, StatusPending, nil, at); err != nil {
			tb.Fatal(err)
		}
		if _, err := stmt.Exec(id, "finish", model, at, at+50, StatusSucceeded, 10, at); err != nil {
			tb.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	return QueryFilter{ScopeID: "scope-a-perf", StartAt: base, EndAt: base.Add(time.Duration(count+1) * time.Second)}
}

func BenchmarkQuery100K(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "usage.sqlite3"), time.Now)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	f := seedPerformanceCalls(b, s, 100000)
	boundaries := make([]time.Time, 601)
	for i := range boundaries {
		boundaries[i] = f.StartAt.Add(time.Duration(i) * f.EndAt.Sub(f.StartAt) / 600)
	}
	boundaries[600] = f.EndAt
	b.Run("Page", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			page, err := s.QueryPage(context.Background(), f, "", 50)
			if err != nil || len(page.Calls) != 50 || page.TotalCount != 100000 {
				b.Fatalf("page=%+v err=%v", page, err)
			}
		}
	})
	b.Run("Overview600Buckets", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			o, err := s.QueryOverview(context.Background(), f, boundaries)
			if err != nil || o.Totals.RequestCount != 100000 || len(o.Buckets) != 600 {
				b.Fatalf("overview requests=%d buckets=%d err=%v", o.Totals.RequestCount, len(o.Buckets), err)
			}
		}
	})
	b.Run("Dimensions", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d, err := s.QueryDimensions(context.Background(), f, "", 1, 50)
			if err != nil || d.TotalCount != 100 || len(d.Groups) != 50 {
				b.Fatalf("dimensions=%+v err=%v", d, err)
			}
		}
	})
	writeSequence := 0
	b.Run("ConcurrentWrites", func(b *testing.B) {
		var maxWrite time.Duration
		for i := 0; i < b.N; i++ {
			ctx, cancel := context.WithCancel(context.Background())
			queryResult := make(chan error, 3)
			go func() { _, err := s.QueryOverview(ctx, f, boundaries); queryResult <- err }()
			go func() { _, err := s.QueryDimensions(ctx, f, "", 1, 50); queryResult <- err }()
			go func() { _, err := s.QueryPage(ctx, f, "", 50); queryResult <- err }()
			var starts []StartRecord
			for n := 0; n < 20; n++ {
				writeSequence++
				start := sampleStart(fmt.Sprintf("concurrent-%d", writeSequence), sampleScope("perf"), f.EndAt.Add(time.Second))
				starts = append(starts, start)
				for _, write := range []func() error{func() error { return s.Begin(ctx, start) }, func() error {
					return s.Finish(ctx, FinishRecord{CallID: start.CallID, Status: StatusSucceeded, FinishedAt: start.StartedAt.Add(time.Second)})
				}} {
					before := time.Now()
					err := write()
					elapsed := time.Since(before)
					if elapsed > maxWrite {
						maxWrite = elapsed
					}
					if err != nil || elapsed >= writeBudget {
						cancel()
						b.Fatalf("write dropped or exceeded 200ms: %v in %v", err, elapsed)
					}
				}
			}
			for n := 0; n < 3; n++ {
				if err := <-queryResult; err != nil {
					cancel()
					b.Fatal(err)
				}
			}
			cancel()
			for _, start := range starts {
				status, err := s.CallStatus(context.Background(), start.CallID)
				if err != nil || status != StatusSucceeded {
					b.Fatalf("dropped finish %s: %s %v", start.CallID, status, err)
				}
			}
		}
		b.ReportMetric(float64(maxWrite.Microseconds())/1000, "max-write-ms")
	})
}
