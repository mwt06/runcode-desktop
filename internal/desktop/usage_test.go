package desktop

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/localusage"
	"github.com/wt68/runcode/internal/protocol"
	hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

func TestUsageShutdownWaitsForInFlightQuery(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	app, _ := newLocalUsageTestApp(t, now)
	queryEntered := make(chan struct{})
	queryResume := make(chan struct{})
	var entered sync.Once
	usageNow = func() time.Time {
		entered.Do(func() {
			close(queryEntered)
			<-queryResume
		})
		return now
	}
	queryDone := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				queryDone <- fmt.Errorf("query panicked: %v", r)
			}
		}()
		_, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "last24h", Timezone: usageTimezone})
		queryDone <- err
	}()
	<-queryEntered
	closed := make(chan struct{})
	go func() {
		app.closeLocalUsage()
		close(closed)
	}()
	select {
	case <-closed:
		t.Error("shutdown completed while an in-flight usage query was blocked")
	case <-time.After(50 * time.Millisecond):
	}
	close(queryResume)
	if err := <-queryDone; err != nil {
		t.Errorf("in-flight query failed: %v", err)
	}
	<-closed
	if _, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "last24h", Timezone: usageTimezone}); err == nil {
		t.Error("query after shutdown should report unavailable")
	}
}

func TestUsageQueriesRaceWithShutdown(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	app, store := newLocalUsageTestApp(t, now)
	scope, err := app.freezeUsageScopeParts("https://bridge.test", "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	start := localusage.StartRecord{CallID: "shutdown-race", Scope: scope, Model: "qwen-max", CallKind: localusage.CallKindLLM, StartedAt: now.Add(-time.Minute)}
	if err := store.Begin(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(context.Background(), localusage.FinishRecord{CallID: start.CallID, Status: localusage.StatusSucceeded, FinishedAt: now, FrozenStart: start}); err != nil {
		t.Fatal(err)
	}
	resolved := protocol.ResolvedUsageQuery{TenantID: "tenant-a", EffectiveRange: protocol.UsageEffectiveRange{
		StartAt: now.Add(-time.Hour).Format(time.RFC3339Nano), EndAt: now.Format(time.RFC3339Nano), Timezone: usageTimezone,
	}}
	queries := []func() error{
		func() error {
			out, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "last24h", Timezone: usageTimezone})
			if err == nil && out.Summary.RequestCount != "1" {
				return fmt.Errorf("incomplete overview: %+v", out.Summary)
			}
			return err
		},
		func() error {
			out, err := app.GetUsageRequests(protocol.UsageRequestsQuery{Query: resolved, PageSize: 20})
			if err == nil && (out.TotalCount != "1" || len(out.Items) != 1 || out.Items[0].ID != start.CallID) {
				return fmt.Errorf("incomplete requests page: %+v", out)
			}
			return err
		},
		func() error {
			out, err := app.GetUsageDimensions(protocol.UsageDimensionsQuery{Query: resolved, GroupBy: "model", Page: 1, PageSize: 50})
			if err == nil && (out.TotalCount != "1" || len(out.Items) != 1 || out.Items[0].ID != start.Model) {
				return fmt.Errorf("incomplete dimensions page: %+v", out)
			}
			return err
		},
		func() error {
			out, err := app.GetLocalUsageTenants()
			if err == nil && (len(out) != 1 || out[0].ID != "tenant-a") {
				return fmt.Errorf("incomplete tenant list: %+v", out)
			}
			return err
		},
	}
	for i, query := range queries {
		if err := query(); err != nil {
			t.Fatalf("query %d before shutdown: %v", i, err)
		}
	}
	var wg sync.WaitGroup
	startGate := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startGate
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("usage query panicked during shutdown: %v", r)
				}
			}()
			for j := 0; j < 100; j++ {
				if err := queries[j%len(queries)](); err != nil {
					var wire *hostproto.Error
					if !errors.As(err, &wire) || wire.Code != protocol.ErrCodeUsageUnavailable {
						t.Errorf("query during shutdown returned unexpected error: %v", err)
						return
					}
				}
			}
		}()
	}
	close(startGate)
	app.closeLocalUsage()
	wg.Wait()
}

func localUsageJWT() string {
	enc := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://issuer.test","sub":"user-1"}`))
	return "eyJhbGciOiJub25lIn0." + enc + ".sig"
}

func newLocalUsageTestApp(t *testing.T, now time.Time) (*App, *localusage.Store) {
	t.Helper()
	isolateConfigDir(t)
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", "https://bridge.test")
	oldNow := usageNow
	usageNow = func() time.Time { return now }
	t.Cleanup(func() { usageNow = oldNow })
	app := New(&recordingSink{})
	app.tokens = newTokenManager("https://issuer.test/token", "fixture", nil, nil)
	app.tokens.setInMemory(tokenSet{AccessToken: localUsageJWT(), Expiry: now.Add(time.Hour)})
	store, err := localusage.Open(filepath.Join(t.TempDir(), "usage.sqlite3"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	app.usageStore = store
	t.Cleanup(func() { _ = store.Close() })
	return app, store
}

func TestLocalUsageCommandsReadSQLiteOnly(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	app, store := newLocalUsageTestApp(t, now)
	scope, err := app.freezeUsageScopeParts("https://bridge.test", "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	start := localusage.StartRecord{CallID: "local-call-1", Scope: scope, Model: "doubao-seedream-4-0-250828", CallKind: localusage.CallKindImage, StartedAt: now.Add(-time.Hour)}
	if err := store.Begin(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	images := int64(1)
	if err := store.Finish(context.Background(), localusage.FinishRecord{CallID: start.CallID, Status: localusage.StatusSucceeded, FinishedAt: now.Add(-30 * time.Minute), GeneratedImages: &images, FrozenStart: start}); err != nil {
		t.Fatal(err)
	}

	overview, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "last24h", Timezone: usageTimezone})
	if err != nil {
		t.Fatal(err)
	}
	if overview.SourceKind != localusage.SourceKindLocalPlatform || overview.Summary.RequestCount != "1" || !overview.Empty && overview.Summary.TotalTokens != nil {
		t.Fatalf("overview = %+v", overview)
	}
	requests, err := app.GetUsageRequests(protocol.UsageRequestsQuery{Query: protocol.ResolvedUsageQuery{TenantID: "tenant-a", EffectiveRange: overview.EffectiveRange}, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(requests.Items) != 1 || requests.Items[0].Model != start.Model || requests.Items[0].MeterKind != "images" {
		t.Fatalf("requests = %+v", requests)
	}
	dims, err := app.GetUsageDimensions(protocol.UsageDimensionsQuery{Query: protocol.ResolvedUsageQuery{TenantID: "tenant-a", EffectiveRange: overview.EffectiveRange}, GroupBy: "model", Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(dims.Items) != 1 || dims.Items[0].ID != start.Model {
		t.Fatalf("dimensions = %+v", dims)
	}
	tenants, err := app.GetLocalUsageTenants()
	if err != nil {
		t.Fatal(err)
	}
	if len(tenants) != 1 || tenants[0].ID != "tenant-a" {
		t.Fatalf("tenants = %+v", tenants)
	}
}

func TestLocalUsageRangeUsesFixedShanghaiZone(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	r, ir, err := localUsageRange(protocol.UsageQuery{TenantID: "wjtest", Range: "last30d", Timezone: usageTimezone}, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Timezone != usageTimezone || !ir.start.Before(ir.end) {
		t.Fatalf("range = %+v ir=%+v", r, ir)
	}
	// FixedZone 不依赖系统 zoneinfo；正式 Windows 包也要能算出上海时间。
	if _, offset := now.In(usageLoc).Zone(); offset != 8*3600 {
		t.Fatalf("usageLoc offset = %d", offset)
	}
}

func TestLocalUsageRejectsProviderDimension(t *testing.T) {
	app, _ := newLocalUsageTestApp(t, time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC))
	_, err := app.GetUsageDimensions(protocol.UsageDimensionsQuery{Query: protocol.ResolvedUsageQuery{TenantID: "tenant-a", EffectiveRange: protocol.UsageEffectiveRange{StartAt: "2026-11-12T00:00:00Z", EndAt: "2026-11-14T00:00:00Z", Timezone: usageTimezone}}, GroupBy: "provider", Page: 1, PageSize: 50})
	if err == nil {
		t.Fatal("provider dimension should be unsupported for local records")
	}
}

func TestLocalUsageReportsCacheHitRateFromCoreUsage(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	app, store := newLocalUsageTestApp(t, now)
	scope, err := app.freezeUsageScopeParts("https://bridge.test", "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	start := localusage.StartRecord{CallID: "local-cache-1", Scope: scope, Model: "qwen3.6-27b", CallKind: localusage.CallKindLLM, StartedAt: now.Add(-time.Hour)}
	if err := store.Begin(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	in, out, hit, miss := int64(100), int64(20), int64(30), int64(70)
	if err := store.Finish(context.Background(), localusage.FinishRecord{CallID: start.CallID, Status: localusage.StatusSucceeded, FinishedAt: now.Add(-time.Minute), InputTokens: &in, InputTokensKnown: true, OutputTokens: &out, OutputTokensKnown: true, CacheHitTokens: &hit, CacheHitTokensKnown: true, CacheMissTokens: &miss, CacheMissTokensKnown: true, FrozenStart: start}); err != nil {
		t.Fatal(err)
	}
	overview, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "last24h", Timezone: usageTimezone})
	if err != nil {
		t.Fatal(err)
	}
	if !overview.Capabilities.CacheMetrics || overview.Summary.CacheHitRate == nil || *overview.Summary.CacheHitRate != 0.3 || overview.Summary.CacheHitTokens == nil || *overview.Summary.CacheHitTokens != "30" {
		t.Fatalf("cache summary = %+v", overview.Summary)
	}
}

func TestLocalUsageReportsConservativeEstimatedCacheRate(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	app, store := newLocalUsageTestApp(t, now)
	scope, err := app.freezeUsageScopeParts("https://bridge.test", "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	write := func(id string, started time.Time, in, out int64) {
		t.Helper()
		start := localusage.StartRecord{CallID: id, Scope: scope, Model: "qwen-max", CallKind: localusage.CallKindLLM, StartedAt: started}
		if err := store.Begin(context.Background(), start); err != nil {
			t.Fatal(err)
		}
		if err := store.Finish(context.Background(), localusage.FinishRecord{
			CallID: id, Status: localusage.StatusSucceeded, FinishedAt: started.Add(time.Second),
			InputTokens: &in, InputTokensKnown: true, OutputTokens: &out, OutputTokensKnown: true, FrozenStart: start,
		}); err != nil {
			t.Fatal(err)
		}
	}
	write("est-1", now.Add(-2*time.Minute), 100, 10)
	write("est-2", now.Add(-time.Minute), 120, 12)
	overview, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "last24h", Timezone: usageTimezone})
	if err != nil {
		t.Fatal(err)
	}
	if !overview.Capabilities.CacheMetrics || !overview.Summary.Quality.ContainsEstimates {
		t.Fatalf("expected estimated cache metrics: %+v", overview.Summary)
	}
	if overview.Summary.CacheHitRate == nil || *overview.Summary.CacheHitRate < 0.833 || *overview.Summary.CacheHitRate > 0.834 {
		t.Fatalf("estimated rate = %v want ~0.833", overview.Summary.CacheHitRate)
	}
	if overview.Summary.CacheHitTokens == nil || *overview.Summary.CacheHitTokens != "100" || overview.Summary.CacheMissTokens == nil || *overview.Summary.CacheMissTokens != "20" {
		t.Fatalf("estimated tokens = %+v", overview.Summary)
	}
	page, err := app.GetUsageRequests(protocol.UsageRequestsQuery{
		Query: protocol.ResolvedUsageQuery{
			TenantID: "tenant-a", SnapshotSeq: overview.SnapshotSeq,
			EffectiveRange: overview.EffectiveRange,
		},
		PageSize: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawEstimated bool
	for _, row := range page.Items {
		if row.ID == "est-2" {
			sawEstimated = true
			if row.Quality.CacheSource != "estimated" {
				t.Fatalf("row est-2 cache source = %q", row.Quality.CacheSource)
			}
		}
	}
	if !sawEstimated {
		t.Fatal("missing estimated request row")
	}
}

func TestLocalUsageTrendBucketsByHour(t *testing.T) {
	// 上海 12:00 = UTC 04:00；「当天」应从 00:00+08 起按小时分桶，不能整段压成一根柱。
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	app, store := newLocalUsageTestApp(t, now)
	scope, err := app.freezeUsageScopeParts("https://bridge.test", "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	write := func(id string, started time.Time, in, out int64) {
		t.Helper()
		start := localusage.StartRecord{CallID: id, Scope: scope, Model: "qwen-max", CallKind: localusage.CallKindLLM, StartedAt: started}
		if err := store.Begin(context.Background(), start); err != nil {
			t.Fatal(err)
		}
		if err := store.Finish(context.Background(), localusage.FinishRecord{
			CallID: id, Status: localusage.StatusSucceeded, FinishedAt: started.Add(time.Minute),
			InputTokens: &in, InputTokensKnown: true, OutputTokens: &out, OutputTokensKnown: true, FrozenStart: start,
		}); err != nil {
			t.Fatal(err)
		}
	}
	write("h1", time.Date(2026, 11, 13, 1, 10, 0, 0, time.UTC), 100, 10) // 09:10+08
	write("h2", time.Date(2026, 11, 13, 3, 20, 0, 0, time.UTC), 200, 20) // 11:20+08

	overview, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "today", Timezone: usageTimezone})
	if err != nil {
		t.Fatal(err)
	}
	// 上海当天 00:00→12:00，按小时应有 12 个桶（末桶裁到当前时刻）。
	if len(overview.Trend) != 12 {
		t.Fatalf("trend len = %d want 12: %+v", len(overview.Trend), overview.Trend)
	}
	var nonempty int
	for _, b := range overview.Trend {
		if b.RequestCount != "0" {
			nonempty++
			if b.Coverage != "known" || b.InputTokens == nil {
				t.Fatalf("bucket = %+v", b)
			}
		}
	}
	if nonempty != 2 {
		t.Fatalf("non-empty buckets = %d trend=%+v", nonempty, overview.Trend)
	}
	// 第二根有数据的桶应是 09:00–10:00+08
	found := false
	for _, b := range overview.Trend {
		start, ok1 := localUsageTime(b.StartAt)
		end, ok2 := localUsageTime(b.EndAt)
		if !ok1 || !ok2 {
			t.Fatalf("bad bucket times %+v", b)
		}
		if start.In(usageLoc).Hour() == 9 && end.In(usageLoc).Hour() == 10 && b.RequestCount == "1" {
			found = true
			if b.InputTokens == nil || *b.InputTokens != "100" {
				t.Fatalf("09:00 bucket tokens = %+v", b)
			}
		}
	}
	if !found {
		t.Fatalf("missing 09:00+08 bucket: %+v", overview.Trend)
	}
}

func TestLocalUsageTotalsIgnoresPendingAndShowsPartialTotal(t *testing.T) {
	// 主回合已入账、自动标题仍 pending：请求数计 1，消耗 Token 显示已知合计，不能是 —。
	now := time.Date(2026, 11, 13, 8, 34, 0, 0, time.UTC)
	app, store := newLocalUsageTestApp(t, now)
	scope, err := app.freezeUsageScopeParts("https://bridge.test", "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	main := localusage.StartRecord{CallID: "main-1", Scope: scope, Model: "qwen3.6-27b", CallKind: localusage.CallKindLLM, StartedAt: now.Add(-2 * time.Second)}
	if err := store.Begin(context.Background(), main); err != nil {
		t.Fatal(err)
	}
	in, out := int64(8561), int64(101)
	if err := store.Finish(context.Background(), localusage.FinishRecord{
		CallID: main.CallID, Status: localusage.StatusSucceeded, FinishedAt: now.Add(-time.Second),
		InputTokens: &in, InputTokensKnown: true, OutputTokens: &out, OutputTokensKnown: true, FrozenStart: main,
	}); err != nil {
		t.Fatal(err)
	}
	title := localusage.StartRecord{CallID: "title-1", Scope: scope, Model: "qwen3.6-27b", CallKind: localusage.CallKindLLM, StartedAt: now.Add(-500 * time.Millisecond)}
	if err := store.Begin(context.Background(), title); err != nil {
		t.Fatal(err)
	}

	overview, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "today", Timezone: usageTimezone})
	if err != nil {
		t.Fatal(err)
	}
	if overview.Summary.RequestCount != "1" {
		t.Fatalf("requestCount=%q want 1 (pending title excluded)", overview.Summary.RequestCount)
	}
	if overview.Summary.TotalTokens == nil || *overview.Summary.TotalTokens != "8662" {
		t.Fatalf("totalTokens=%v want 8662", overview.Summary.TotalTokens)
	}
	if overview.Summary.InputTokens == nil || *overview.Summary.InputTokens != "8561" {
		t.Fatalf("inputTokens=%v", overview.Summary.InputTokens)
	}
	requests, err := app.GetUsageRequests(protocol.UsageRequestsQuery{
		Query: protocol.ResolvedUsageQuery{TenantID: "tenant-a", EffectiveRange: overview.EffectiveRange}, PageSize: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(requests.Items) != 1 || requests.Items[0].ID != "main-1" {
		t.Fatalf("request log = %+v, pending title must be hidden", requests.Items)
	}
}

func TestLocalUsageTrendAllStartsAtEarliestCall(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	app, store := newLocalUsageTestApp(t, now)
	scope, err := app.freezeUsageScopeParts("https://bridge.test", "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	started := now.Add(-3 * time.Hour)
	start := localusage.StartRecord{CallID: "all-1", Scope: scope, Model: "qwen-max", CallKind: localusage.CallKindLLM, StartedAt: started}
	if err := store.Begin(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	in, out := int64(10), int64(2)
	if err := store.Finish(context.Background(), localusage.FinishRecord{CallID: start.CallID, Status: localusage.StatusSucceeded, FinishedAt: started.Add(time.Minute), InputTokens: &in, InputTokensKnown: true, OutputTokens: &out, OutputTokensKnown: true, FrozenStart: start}); err != nil {
		t.Fatal(err)
	}
	overview, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "all", Timezone: usageTimezone})
	if err != nil {
		t.Fatal(err)
	}
	effStart, ok := localUsageTime(overview.EffectiveRange.StartAt)
	if !ok || effStart.UnixMilli() != started.UTC().UnixMilli() {
		t.Fatalf("effective start = %v want %v", overview.EffectiveRange.StartAt, started.UTC())
	}
	if len(overview.Trend) == 0 || len(overview.Trend) > usageMaxTrendBuckets {
		t.Fatalf("trend len = %d", len(overview.Trend))
	}
}
