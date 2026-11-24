package localusage

import (
	"context"
	"testing"
	"time"
)

func TestQueryPageUsesLatestFinishAndStableWatermark(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	s := openTemp(t, testClock(t, now))
	ctx := context.Background()
	scope := sampleScope("query")
	start := sampleStart("call-a", scope, now.Add(-2*time.Hour))
	if err := s.Begin(ctx, start); err != nil {
		t.Fatal(err)
	}
	input, output := int64(100), int64(20)
	hit, miss := int64(30), int64(70)
	if err := s.Finish(ctx, FinishRecord{
		CallID: "call-a", Status: StatusSucceeded, FinishedAt: now.Add(-time.Hour),
		InputTokens: &input, InputTokensKnown: true, OutputTokens: &output, OutputTokensKnown: true,
		CacheHitTokens: &hit, CacheHitTokensKnown: true, CacheMissTokens: &miss, CacheMissTokensKnown: true,
		DurationMs: ptrInt64(120), FrozenStart: start,
	}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := s.QueryPage(ctx, QueryFilter{ScopeID: scope.ScopeID, StartAt: now.Add(-3 * time.Hour), EndAt: now}, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Seq == 0 || len(snapshot.Calls) != 1 || snapshot.Calls[0].Status != StatusSucceeded {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if !snapshot.Calls[0].CacheHitTokensKnown || snapshot.Calls[0].CacheHitTokens == nil || *snapshot.Calls[0].CacheHitTokens != 30 {
		t.Fatalf("cache hit = %+v", snapshot.Calls[0])
	}

	// 旧水位之后的 finish 不能改变已读快照；新查询才看到它。
	late := int64(999)
	if err := s.Finish(ctx, FinishRecord{CallID: "call-a", Status: StatusFailed, FinishedAt: now, InputTokens: &late, InputTokensKnown: true, FrozenStart: start}); err != nil {
		t.Fatal(err)
	}
	old, err := s.QueryPage(ctx, QueryFilter{ScopeID: scope.ScopeID, StartAt: now.Add(-3 * time.Hour), EndAt: now, SnapshotSeq: snapshot.Seq}, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if old.Calls[0].Status != StatusSucceeded || old.Calls[0].InputTokens == nil || *old.Calls[0].InputTokens != 100 {
		t.Fatalf("old snapshot changed: %+v", old.Calls[0])
	}
}

func TestQueryPageCursorAndHistoricalTenants(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	s := openTemp(t, testClock(t, now))
	ctx := context.Background()
	for i, tenant := range []string{"tenant-a", "tenant-b"} {
		scope := sampleScope(tenant)
		scope.AccountDigest, scope.BridgeDigest = "same-account", "same-bridge"
		scope.TenantID = tenant
		count := 1
		if tenant == "tenant-a" {
			count = 2
		}
		for n := 0; n < count; n++ {
			start := sampleStart("call-"+tenant+"-"+string(rune('a'+n)), scope, now.Add(-time.Duration(i+n+1)*time.Hour))
			if err := s.Begin(ctx, start); err != nil {
				t.Fatal(err)
			}
			if err := s.Finish(ctx, FinishRecord{CallID: start.CallID, Status: StatusSucceeded, FinishedAt: now, FrozenStart: start}); err != nil {
				t.Fatal(err)
			}
		}
	}
	page, err := s.QueryPage(ctx, QueryFilter{ScopeID: sampleScope("tenant-a").ScopeID, StartAt: now.Add(-24 * time.Hour), EndAt: now.Add(time.Hour)}, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Calls) != 1 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("page = %+v", page)
	}
	if _, err := s.QueryPage(ctx, QueryFilter{ScopeID: sampleScope("tenant-a").ScopeID, StartAt: now.Add(-24 * time.Hour), EndAt: now.Add(time.Hour)}, page.NextCursor, 1); err != nil {
		t.Fatal(err)
	}
	tenants, err := s.ListTenantIDs(ctx, "same-account", "same-bridge")
	if err != nil {
		t.Fatal(err)
	}
	if len(tenants) != 2 || tenants[0] != "tenant-a" || tenants[1] != "tenant-b" {
		t.Fatalf("tenants = %#v", tenants)
	}
}

func ptrInt64(v int64) *int64 { return &v }
