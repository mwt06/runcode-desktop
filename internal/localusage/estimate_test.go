package localusage

import (
	"context"
	"testing"
	"time"
)

func writeTokenCall(t *testing.T, s *Store, scope Scope, id, model string, started time.Time, in, out int64, hit, miss *int64) {
	t.Helper()
	start := StartRecord{CallID: id, Scope: scope, Model: model, CallKind: CallKindLLM, StartedAt: started}
	if err := s.Begin(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	finish := FinishRecord{
		CallID: id, Status: StatusSucceeded, FinishedAt: started.Add(time.Second),
		InputTokens: &in, InputTokensKnown: true, OutputTokens: &out, OutputTokensKnown: true, FrozenStart: start,
	}
	if hit != nil && miss != nil {
		finish.CacheHitTokens, finish.CacheHitTokensKnown = hit, true
		finish.CacheMissTokens, finish.CacheMissTokensKnown = miss, true
	}
	if err := s.Finish(context.Background(), finish); err != nil {
		t.Fatal(err)
	}
}

func TestLocalUsageEstimateContinuousCalls(t *testing.T) {
	base := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	s := openTemp(t, testClock(t, base))
	scope := sampleScope("est-cont")
	writeTokenCall(t, s, scope, "a", "qwen-max", base, 100, 10, nil, nil)
	writeTokenCall(t, s, scope, "b", "qwen-max", base.Add(time.Minute), 120, 12, nil, nil)

	filter := QueryFilter{ScopeID: scope.ScopeID, StartAt: base.Add(-time.Hour), EndAt: base.Add(time.Hour)}
	overview, err := s.QueryOverview(context.Background(), filter, nil)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Totals.CacheSampleCount != 1 || overview.Totals.CacheEstimatedCount != 1 {
		t.Fatalf("cache samples=%d estimated=%d want 1/1: %+v", overview.Totals.CacheSampleCount, overview.Totals.CacheEstimatedCount, overview.Totals)
	}
	if overview.Totals.CacheHitTokens != 100 || overview.Totals.CacheMissTokens != 20 {
		t.Fatalf("cache hit/miss = %d/%d want 100/20", overview.Totals.CacheHitTokens, overview.Totals.CacheMissTokens)
	}

	page, err := s.QueryPage(context.Background(), filter, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, call := range page.Calls {
		if call.CallID != "b" {
			continue
		}
		found = true
		if !call.CacheEstimated || call.CacheHitTokens == nil || *call.CacheHitTokens != 100 || call.CacheMissTokens == nil || *call.CacheMissTokens != 20 {
			t.Fatalf("call b = %+v", call)
		}
	}
	if !found {
		t.Fatal("missing call b")
	}
}

func TestLocalUsageCacheEstimateIsMaterializedAtFinish(t *testing.T) {
	base := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	s := openTemp(t, testClock(t, base))
	scope := sampleScope("est-materialized")
	writeTokenCall(t, s, scope, "a", "qwen-max", base, 100, 10, nil, nil)
	writeTokenCall(t, s, scope, "b", "qwen-max", base.Add(time.Minute), 120, 12, nil, nil)

	var hit, miss, estimated int64
	err := s.db.QueryRow(`SELECT cache_hit_tokens, cache_miss_tokens, estimated
		FROM cache_estimates WHERE call_id=?`, "b").Scan(&hit, &miss, &estimated)
	if err != nil {
		t.Fatal(err)
	}
	if hit != 100 || miss != 20 || estimated != 1 {
		t.Fatalf("materialized estimate hit/miss/estimated = %d/%d/%d", hit, miss, estimated)
	}
}

func TestLocalUsageEstimateFirstCallStaysUnknown(t *testing.T) {
	base := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	s := openTemp(t, testClock(t, base))
	scope := sampleScope("est-first")
	writeTokenCall(t, s, scope, "only", "qwen-max", base, 100, 10, nil, nil)
	overview, err := s.QueryOverview(context.Background(), QueryFilter{ScopeID: scope.ScopeID, StartAt: base.Add(-time.Hour), EndAt: base.Add(time.Hour)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Totals.CacheSampleCount != 0 || overview.Totals.CacheEstimatedCount != 0 || overview.Totals.CacheHitTokens != 0 {
		t.Fatalf("first call should stay unknown: %+v", overview.Totals)
	}
}

func TestLocalUsageEstimateCrossModelIsolated(t *testing.T) {
	base := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	s := openTemp(t, testClock(t, base))
	scope := sampleScope("est-model")
	writeTokenCall(t, s, scope, "a", "model-a", base, 100, 10, nil, nil)
	writeTokenCall(t, s, scope, "b", "model-b", base.Add(time.Minute), 120, 12, nil, nil)
	overview, err := s.QueryOverview(context.Background(), QueryFilter{ScopeID: scope.ScopeID, StartAt: base.Add(-time.Hour), EndAt: base.Add(time.Hour)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Totals.CacheSampleCount != 0 || overview.Totals.CacheEstimatedCount != 0 {
		t.Fatalf("cross-model must not estimate: %+v", overview.Totals)
	}
}

func TestLocalUsageEstimateBeyond30Minutes(t *testing.T) {
	base := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	s := openTemp(t, testClock(t, base))
	scope := sampleScope("est-gap")
	writeTokenCall(t, s, scope, "a", "qwen-max", base, 100, 10, nil, nil)
	writeTokenCall(t, s, scope, "b", "qwen-max", base.Add(31*time.Minute), 120, 12, nil, nil)
	overview, err := s.QueryOverview(context.Background(), QueryFilter{ScopeID: scope.ScopeID, StartAt: base.Add(-time.Hour), EndAt: base.Add(2 * time.Hour)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Totals.CacheSampleCount != 0 || overview.Totals.CacheEstimatedCount != 0 {
		t.Fatalf(">30m must not estimate: %+v", overview.Totals)
	}
}

func TestLocalUsageEstimateReportedPreferred(t *testing.T) {
	base := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	s := openTemp(t, testClock(t, base))
	scope := sampleScope("est-reported")
	writeTokenCall(t, s, scope, "a", "qwen-max", base, 100, 10, nil, nil)
	hit, miss := int64(40), int64(80)
	writeTokenCall(t, s, scope, "b", "qwen-max", base.Add(time.Minute), 120, 12, &hit, &miss)
	overview, err := s.QueryOverview(context.Background(), QueryFilter{ScopeID: scope.ScopeID, StartAt: base.Add(-time.Hour), EndAt: base.Add(time.Hour)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Totals.CacheSampleCount != 1 || overview.Totals.CacheEstimatedCount != 0 {
		t.Fatalf("reported should win: %+v", overview.Totals)
	}
	if overview.Totals.CacheHitTokens != 40 || overview.Totals.CacheMissTokens != 80 {
		t.Fatalf("reported hit/miss = %d/%d", overview.Totals.CacheHitTokens, overview.Totals.CacheMissTokens)
	}
	page, err := s.QueryPage(context.Background(), QueryFilter{ScopeID: scope.ScopeID, StartAt: base.Add(-time.Hour), EndAt: base.Add(time.Hour)}, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range page.Calls {
		if call.CallID == "b" && call.CacheEstimated {
			t.Fatalf("reported row must not be marked estimated: %+v", call)
		}
	}
}
