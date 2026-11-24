package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/localusage"
	"github.com/wt68/runcode/internal/protocol"
	hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

func TestUsageCalendarRangesIncludeToday(t *testing.T) {
	now := time.Date(2026, 11, 15, 7, 23, 45, 0, time.UTC)
	for _, tc := range []struct{ name, start string }{
		{"last7d", "2026-11-08T16:00:00Z"},
		{"last14d", "2026-11-01T16:00:00Z"},
		{"last30d", "2026-10-16T16:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, err := localUsageRange(protocol.UsageQuery{Range: tc.name}, now)
			if err != nil || r.StartAt != tc.start || r.EndAt != "2026-11-15T07:23:45Z" {
				t.Fatalf("range=%+v err=%v", r, err)
			}
		})
	}
}

func writeUsageContractCall(t *testing.T, app *App, store *localusage.Store, id, model, status string, at time.Time, input, output *int64) {
	t.Helper()
	scope, err := app.freezeUsageScopeParts("https://bridge.test", "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	start := localusage.StartRecord{CallID: id, Scope: scope, Model: model, CallKind: localusage.CallKindLLM, StartedAt: at}
	if err := store.Begin(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	if status == localusage.StatusPending {
		return
	}
	if err := store.Finish(context.Background(), localusage.FinishRecord{CallID: id, Status: status, FinishedAt: at.Add(time.Minute), InputTokens: input, InputTokensKnown: input != nil, OutputTokens: output, OutputTokensKnown: output != nil, FrozenStart: start}); err != nil {
		t.Fatal(err)
	}
}

func TestUsagePartialTokensKeepKnownTotalsAndSamples(t *testing.T) {
	now := time.Date(2026, 11, 15, 7, 0, 0, 0, time.UTC)
	app, store := newLocalUsageTestApp(t, now)
	in, out := int64(100), int64(7)
	writeUsageContractCall(t, app, store, "input-only", "model", localusage.StatusSucceeded, now.Add(-3*time.Hour), &in, nil)
	writeUsageContractCall(t, app, store, "output-only", "model", localusage.StatusSucceeded, now.Add(-2*time.Hour), nil, &out)
	writeUsageContractCall(t, app, store, "unknown", "model", localusage.StatusFailed, now.Add(-time.Hour), nil, nil)
	overview, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "today"})
	if err != nil {
		t.Fatal(err)
	}
	totals := overview.Summary
	if totals.TotalTokens == nil || *totals.TotalTokens != "107" || totals.InputTokens == nil || *totals.InputTokens != "100" || totals.OutputTokens == nil || *totals.OutputTokens != "7" {
		t.Fatalf("known amounts hidden: %+v", totals)
	}
	if totals.Quality.TokenCoverage != "partial" || totals.InputTokenSampleCount != "1" || totals.OutputTokenSampleCount != "1" || totals.TotalTokenSampleCount != "2" || totals.TokenRequestCount != "3" {
		t.Fatalf("sample coverage = %+v", totals)
	}
}

func TestUsageAllStartsAtScopeCollectionWithModelFilter(t *testing.T) {
	now := time.Date(2026, 11, 15, 7, 0, 0, 0, time.UTC)
	app, store := newLocalUsageTestApp(t, now)
	writeUsageContractCall(t, app, store, "older-model", "older", localusage.StatusSucceeded, now.Add(-48*time.Hour), nil, nil)
	writeUsageContractCall(t, app, store, "newer-model", "newer", localusage.StatusSucceeded, now.Add(-time.Hour), nil, nil)
	for _, model := range []string{"newer", "no-matches"} {
		overview, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "all", Model: model})
		if err != nil {
			t.Fatal(err)
		}
		if overview.EffectiveRange.StartAt != "2026-11-13T07:00:00Z" || overview.CollectionStartedAt == nil || *overview.CollectionStartedAt != "2026-11-13T07:00:00Z" {
			t.Fatalf("model %s changed scope collection range: %+v", model, overview)
		}
	}
}

func TestUsageEmptyAllDoesNotStartAtEpochOrClaimCoverage(t *testing.T) {
	now := time.Date(2026, 11, 15, 7, 0, 0, 0, time.UTC)
	app, _ := newLocalUsageTestApp(t, now)
	overview, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if overview.EffectiveRange.StartAt != "2026-11-14T16:00:00Z" || overview.CollectionStartedAt != nil || overview.CoverageStatus != "unknown" || overview.SnapshotSeq != "0" {
		t.Fatalf("empty collection = %+v", overview)
	}
}

func TestUsageStatusCountsAndFrozenPages(t *testing.T) {
	now := time.Date(2026, 11, 15, 7, 0, 0, 0, time.UTC)
	app, store := newLocalUsageTestApp(t, now)
	for i, status := range []string{localusage.StatusPending, localusage.StatusSucceeded, localusage.StatusFailed, localusage.StatusCancelled, localusage.StatusUnknown, localusage.StatusSaveFailed} {
		writeUsageContractCall(t, app, store, status, "model", status, now.Add(time.Duration(-i-1)*time.Hour), nil, nil)
	}
	overview, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "today"})
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.UsageStatusCounts{Pending: "1", Succeeded: "1", Failed: "1", Cancelled: "1", Unknown: "1", SaveFailed: "1"}
	if overview.StatusCounts == nil || *overview.StatusCounts != want || overview.Summary.RequestCount != "5" || overview.SnapshotSeq == "" || !overview.Capabilities.RequestStatus {
		t.Fatalf("status distribution = %+v", overview)
	}
	writeUsageContractCall(t, app, store, "late", "late-model", localusage.StatusSucceeded, now.Add(-time.Minute), nil, nil)
	resolved := protocol.ResolvedUsageQuery{TenantID: "tenant-a", EffectiveRange: overview.EffectiveRange, SnapshotSeq: overview.SnapshotSeq}
	page, err := app.GetUsageRequests(protocol.UsageRequestsQuery{Query: resolved, PageSize: 20})
	if err != nil || len(page.Items) != 5 || page.TotalCount != "5" || page.QueryKey != overview.QueryKey {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	for _, row := range page.Items {
		if row.Status == "" || row.ID == "late" || row.ID == "pending" {
			t.Fatalf("unexpected row %+v", row)
		}
	}
	dim, err := app.GetUsageDimensions(protocol.UsageDimensionsQuery{Query: resolved, GroupBy: "model", Page: 1, PageSize: 50})
	if err != nil || len(dim.Items) != 1 || dim.Items[0].Totals.RequestCount != "5" || dim.QueryKey != overview.QueryKey {
		t.Fatalf("dimensions=%+v err=%v", dim, err)
	}
}

func TestUsageEmptySnapshotRemainsEmptyAfterNewCalls(t *testing.T) {
	now := time.Date(2026, 11, 15, 7, 0, 0, 0, time.UTC)
	app, store := newLocalUsageTestApp(t, now)
	overview, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "today"})
	if err != nil {
		t.Fatal(err)
	}
	writeUsageContractCall(t, app, store, "new", "model", localusage.StatusSucceeded, now.Add(-time.Minute), nil, nil)
	resolved := protocol.ResolvedUsageQuery{TenantID: "tenant-a", EffectiveRange: overview.EffectiveRange, SnapshotSeq: overview.SnapshotSeq}
	page, err := app.GetUsageRequests(protocol.UsageRequestsQuery{Query: resolved, PageSize: 20})
	if err != nil || page.TotalCount != "0" || len(page.Items) != 0 || page.HasMore {
		t.Fatalf("empty snapshot page=%+v err=%v", page, err)
	}
	dims, err := app.GetUsageDimensions(protocol.UsageDimensionsQuery{Query: resolved, GroupBy: "model", Page: 1, PageSize: 50})
	if err != nil || dims.TotalCount != "0" || len(dims.Items) != 0 || dims.HasMore {
		t.Fatalf("empty snapshot dims=%+v err=%v", dims, err)
	}
}

func TestUsageTrendCalendarBoundariesAndLimit(t *testing.T) {
	start := time.Date(2000, 1, 1, 0, 0, 0, 0, usageLoc)
	for _, tc := range []struct {
		name    string
		end     time.Time
		buckets int
		fail    bool
	}{
		{"hour", start.Add(90 * time.Minute), 2, false},
		{"day", start.AddDate(0, 0, 8), 8, false},
		{"month", start.AddDate(0, 4, 0), 4, false},
		{"600 months", start.AddDate(50, 0, 0), 600, false},
		{"over limit", start.AddDate(50, 1, 0), 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := localUsageBoundaries(start, tc.end)
			if tc.fail {
				if err == nil {
					t.Fatal("accepted over 600 buckets")
				}
				return
			}
			if err != nil || len(b) != tc.buckets+1 || !b[0].Equal(start) || !b[len(b)-1].Equal(tc.end) {
				t.Fatalf("boundaries=%v err=%v", b, err)
			}
		})
	}
}

func TestUsageUnavailableIdentityDoesNotBecomeZeroUsage(t *testing.T) {
	now := time.Date(2026, 11, 15, 7, 0, 0, 0, time.UTC)
	app, _ := newLocalUsageTestApp(t, now)
	app.tokens.setInMemory(tokenSet{AccessToken: "opaque", Expiry: time.Now().Add(time.Hour)})
	_, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "today"})
	var wire *hostproto.Error
	if !errors.As(err, &wire) || wire.Code != protocol.ErrCodeUsageUnavailable || !strings.Contains(wire.Message, "账号身份") {
		t.Fatalf("opaque identity error=%v", err)
	}
}

func TestUsageOverviewSurfacesCollectionGap(t *testing.T) {
	now := time.Date(2026, 11, 15, 7, 0, 0, 0, time.UTC)
	app, store := newLocalUsageTestApp(t, now)
	writeUsageContractCall(t, app, store, "observed", "model", localusage.StatusSucceeded, now.Add(-2*time.Hour), nil, nil)
	scope, err := app.freezeUsageScopeParts("https://bridge.test", "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordCoverageGap(context.Background(), localusage.CoverageDiagnostic{At: now.Add(-time.Hour), ScopeID: scope.ScopeID, AccountDigest: scope.AccountDigest, BridgeDigest: scope.BridgeDigest, Reason: "finish_failed"}); err != nil {
		t.Fatal(err)
	}
	o, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if o.CoverageStatus != "gap" || o.StorageStatus != "degraded" || len(o.CoverageGaps) != 1 || o.Summary.RequestCount != "1" {
		t.Fatalf("gap hidden=%+v", o)
	}
	var gapBuckets int
	for _, bucket := range o.Trend {
		if bucket.Coverage == "gap" {
			gapBuckets++
		}
	}
	if gapBuckets != 1 {
		t.Fatalf("gap buckets=%d", gapBuckets)
	}
}
