package desktop

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/wt68/runcode/internal/localusage"
	"github.com/wt68/runcode/internal/protocol"
)

// 本文件是 S09 本机用量查询层。采集事件已经冻结了账号、Bridge、租户和模型，
// 这里仅按当前查询身份读取 SQLite；不请求 Bridge，也不接受前端传入的账号摘要。

const localUsageMappingVersion = "s09-local-v1"

func validLocalTenant(s string) bool {
	if s == "" || len([]rune(s)) > 128 {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == ':' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func (a *App) localUsageIdentity() (string, error) {
	if a.tokens == nil || !a.tokens.LoggedIn() {
		return "", usageNotLoggedIn()
	}
	issuer, sub := a.tokens.identityParts()
	if issuer == "" || sub == "" {
		return "", usageError(protocol.ErrCodeUsageUnavailable, "当前登录凭据缺少可验证的账号身份，无法归属本机用量；请重新登录通行证")
	}
	return digestPair(issuer, sub), nil
}

func (a *App) localUsageScope(tenant string) (localusage.Scope, error) {
	if !validLocalTenant(tenant) {
		return localusage.Scope{}, usageInvalidArgument("请先选择一个租户")
	}
	account, err := a.localUsageIdentity()
	if err != nil {
		return localusage.Scope{}, err
	}
	bridge := passportConfig().BridgeBaseURL
	return localusage.Scope{
		ScopeID:       digestPair(account, digestBridge(bridge)+":"+tenant),
		AccountDigest: account,
		BridgeDigest:  digestBridge(bridge),
		TenantID:      tenant,
	}, nil
}

func localUsageTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

func localUsageRange(q protocol.UsageQuery, now time.Time) (protocol.UsageEffectiveRange, usageInstantRange, error) {
	if q.Timezone != "" && q.Timezone != usageTimezone {
		return protocol.UsageEffectiveRange{}, usageInstantRange{}, usageInvalidArgument("统计时区固定为 Asia/Shanghai")
	}
	loc := usageLoc
	n := now.In(loc)
	end := n
	var start time.Time
	if q.Range != "custom" && (q.StartAt != "" || q.EndAt != "") {
		return protocol.UsageEffectiveRange{}, usageInstantRange{}, usageInvalidArgument("快捷范围不接受起止时间")
	}
	switch q.Range {
	case "today":
		start = time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	case "last24h":
		start = n.Add(-24 * time.Hour)
	case "last7d":
		start = time.Date(n.Year(), n.Month(), n.Day()-6, 0, 0, 0, 0, loc)
	case "last14d":
		start = time.Date(n.Year(), n.Month(), n.Day()-13, 0, 0, 0, 0, loc)
	case "last30d":
		start = time.Date(n.Year(), n.Month(), n.Day()-29, 0, 0, 0, 0, loc)
	case "all":
		// 未开始采集时使用当天范围；已有 scope 的起点在读取元数据后固定。
		start = time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	case "custom":
		var ok bool
		if start, ok = localUsageTime(q.StartAt); !ok {
			return protocol.UsageEffectiveRange{}, usageInstantRange{}, usageInvalidArgument("请填写带时区的开始时间")
		}
		if q.FollowNow {
			if q.EndAt != "" {
				return protocol.UsageEffectiveRange{}, usageInstantRange{}, usageInvalidArgument("跟随当前时刻时不能再指定结束时间")
			}
		} else if end, ok = localUsageTime(q.EndAt); !ok {
			return protocol.UsageEffectiveRange{}, usageInstantRange{}, usageInvalidArgument("请填写带时区的结束时间")
		}
	default:
		return protocol.UsageEffectiveRange{}, usageInstantRange{}, usageInvalidArgument("时间范围无效")
	}
	latest := now.Add(2 * time.Minute)
	if !start.Before(latest) || end.After(latest) {
		return protocol.UsageEffectiveRange{}, usageInstantRange{}, usageInvalidArgument("查询时间不能晚于当前时刻")
	}
	if !start.Before(end) {
		return protocol.UsageEffectiveRange{}, usageInstantRange{}, usageInvalidArgument("开始时间必须早于结束时间")
	}
	r := protocol.UsageEffectiveRange{StartAt: start.UTC().Format(time.RFC3339Nano), EndAt: end.UTC().Format(time.RFC3339Nano), Timezone: usageTimezone}
	return r, usageInstantRange{start: start.UTC(), end: end.UTC()}, nil
}

func localResolvedRange(q protocol.ResolvedUsageQuery) (protocol.UsageEffectiveRange, usageInstantRange, error) {
	r := q.EffectiveRange
	start, ok1 := localUsageTime(r.StartAt)
	end, ok2 := localUsageTime(r.EndAt)
	if !ok1 || !ok2 || !start.Before(end) || (r.Timezone != "" && r.Timezone != usageTimezone) {
		return protocol.UsageEffectiveRange{}, usageInstantRange{}, usageInvalidArgument("查询范围无效，请刷新总览后重试")
	}
	r.Timezone = usageTimezone
	return r, usageInstantRange{start: start.UTC(), end: end.UTC()}, nil
}

func (a *App) localUsageFilter(tenant, model string, r usageInstantRange) (localusage.QueryFilter, error) {
	if model != "" && !validUsageText(model, usageMaxTextLen) {
		return localusage.QueryFilter{}, usageInvalidArgument("模型筛选条件无效")
	}
	scope, err := a.localUsageScope(tenant)
	if err != nil {
		return localusage.QueryFilter{}, err
	}
	return localusage.QueryFilter{ScopeID: scope.ScopeID, StartAt: r.start, EndAt: r.end, Model: model}, nil
}

// localUsageTotals maps SQL aggregates without dropping partially known tokens.
func localUsageTotals(a localusage.AggregateTotals) protocol.UsageTotals {
	value := func(sum, samples int64) *string {
		if samples == 0 {
			return nil
		}
		v := strconv.FormatInt(sum, 10)
		return &v
	}
	coverage := "none"
	if a.TokenCallCount > 0 && a.CompleteTokenSampleCount == a.TokenCallCount {
		coverage = "complete"
	} else if a.TokenSampleCount > 0 {
		coverage = "partial"
	}
	cacheCoverage := "none"
	if a.TokenCallCount > 0 && a.CacheSampleCount == a.TokenCallCount {
		cacheCoverage = "complete"
	} else if a.CacheSampleCount > 0 {
		cacheCoverage = "partial"
	}
	var avg, rate *float64
	if a.DurationSampleCount > 0 {
		v := float64(a.DurationSumMs) / float64(a.DurationSampleCount)
		avg = &v
	}
	if a.CacheSampleCount > 0 && a.CacheEligibleInputTokens > 0 {
		v := float64(a.CacheHitTokens) / float64(a.CacheEligibleInputTokens)
		rate = &v
	}
	return protocol.UsageTotals{
		RequestCount: strconv.FormatInt(a.RequestCount, 10), TokenRequestCount: strconv.FormatInt(a.TokenCallCount, 10),
		InputTokenSampleCount: strconv.FormatInt(a.InputSampleCount, 10), OutputTokenSampleCount: strconv.FormatInt(a.OutputSampleCount, 10), TotalTokenSampleCount: strconv.FormatInt(a.TokenSampleCount, 10),
		InputTokens: value(a.InputTokens, a.InputSampleCount), OutputTokens: value(a.OutputTokens, a.OutputSampleCount), TotalTokens: value(a.TotalTokens, a.TokenSampleCount),
		CacheHitTokens: value(a.CacheHitTokens, a.CacheSampleCount), CacheMissTokens: value(a.CacheMissTokens, a.CacheSampleCount), CacheEligibleInputTokens: value(a.CacheEligibleInputTokens, a.CacheSampleCount),
		CacheHitRate: rate, CacheKnownRequestCount: strconv.FormatInt(a.CacheSampleCount, 10), CacheEligibleRequestCount: strconv.FormatInt(a.TokenCallCount, 10),
		DurationSampleCount: strconv.FormatInt(a.DurationSampleCount, 10), AverageDurationMs: avg,
		Quality: protocol.UsageQuality{
			TokenCoverage: coverage, CacheCoverage: cacheCoverage,
			ContainsEstimates: a.CacheEstimatedCount > 0, InvalidRecordCount: "0",
		},
	}
}

const usageMaxTrendBuckets = 600

// localUsageGranularity 与前端 bucketGranularity / 设计说明一致：
// ≤48 小时按小时，≤90 天按天，更长按月。
func localUsageGranularity(start, end time.Time) string {
	span := end.Sub(start)
	if span <= 48*time.Hour {
		return "hour"
	}
	if span <= 90*24*time.Hour {
		return "day"
	}
	return "month"
}

func localUsageAlignFloor(t time.Time, g string) time.Time {
	t = t.In(usageLoc)
	switch g {
	case "hour":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, usageLoc)
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, usageLoc)
	default:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, usageLoc)
	}
}

func localUsageNextBoundary(aligned time.Time, g string) time.Time {
	aligned = aligned.In(usageLoc)
	switch g {
	case "hour":
		return aligned.Add(time.Hour)
	case "day":
		return aligned.AddDate(0, 0, 1)
	default:
		return aligned.AddDate(0, 1, 0)
	}
}

// localUsageBoundaries fixes calendar buckets before SQLite aggregates them.
func localUsageBoundaries(start, end time.Time) ([]time.Time, error) {
	if !start.Before(end) {
		return nil, usageError(protocol.ErrCodeUsageInvalidRange, "开始时间必须早于结束时间")
	}
	g := localUsageGranularity(start, end)
	out := []time.Time{start}
	for cur := localUsageAlignFloor(start, g); cur.Before(end); {
		next := localUsageNextBoundary(cur, g)
		if next.After(end) {
			next = end
		}
		out = append(out, next)
		if len(out) > usageMaxTrendBuckets+1 {
			return nil, usageError(protocol.ErrCodeUsageInvalidRange, "趋势分桶超过 600 个，请缩小时间范围")
		}
		cur = next
	}
	return out, nil
}

func localUsageStatusCounts(s localusage.StatusTotals) *protocol.UsageStatusCounts {
	return &protocol.UsageStatusCounts{
		Pending: strconv.FormatInt(s.Pending, 10), Succeeded: strconv.FormatInt(s.Succeeded, 10),
		Failed: strconv.FormatInt(s.Failed, 10), Cancelled: strconv.FormatInt(s.Cancelled, 10),
		Unknown: strconv.FormatInt(s.Unknown, 10), SaveFailed: strconv.FormatInt(s.SaveFailed, 10),
	}
}

func localUsageQueryError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return usageError(protocol.ErrCodeUsageTimeout, "本机用量查询超时，请缩小时间范围后重试")
	}
	if errors.Is(err, localusage.ErrInvalidCursor) || errors.Is(err, localusage.ErrInvalidSnapshot) {
		return usageError(protocol.ErrCodeUsageInvalidCursor, "分页已失效，请回到第一页重新查看")
	}
	return usageUnavailable("")
}

func pinLocalUsageSnapshot(f *localusage.QueryFilter, raw string) error {
	if raw == "" {
		return nil
	}
	seq, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seq < 0 || strconv.FormatInt(seq, 10) != raw {
		return usageError(protocol.ErrCodeUsageInvalidCursor, "查询快照无效，请刷新总览后重试")
	}
	f.SnapshotSeq, f.SnapshotPinned = seq, true
	return nil
}

func (a *App) localUsageOverview(q protocol.UsageQuery) (protocol.UsageOverview, error) {
	if q.ProviderID != "" {
		return protocol.UsageOverview{}, usageError(protocol.ErrCodeUsageUnsupported, "本机记录暂不提供供应商筛选")
	}
	now := usageNow()
	r, ir, err := localUsageRange(q, now)
	if err != nil {
		return protocol.UsageOverview{}, err
	}
	if q.Model != "" && !validUsageText(q.Model, usageMaxTextLen) {
		return protocol.UsageOverview{}, usageInvalidArgument("模型筛选条件无效")
	}
	scope, err := a.localUsageScope(q.TenantID)
	if err != nil {
		return protocol.UsageOverview{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	coverageStart := ir.start
	if q.Range == "all" {
		coverageStart = time.Time{}
	}
	coverage, err := a.usageStore.QueryCoverage(ctx, scope, coverageStart, ir.end)
	if err != nil {
		return protocol.UsageOverview{}, localUsageQueryError(err)
	}
	if q.Range == "all" && coverage.CollectionStartedAt != nil && coverage.CollectionStartedAt.Before(ir.end) {
		ir.start = coverage.CollectionStartedAt.UTC()
		r.StartAt = ir.start.Format(time.RFC3339Nano)
	}
	boundaries, err := localUsageBoundaries(ir.start, ir.end)
	if err != nil {
		return protocol.UsageOverview{}, err
	}
	f := localusage.QueryFilter{ScopeID: scope.ScopeID, StartAt: ir.start, EndAt: ir.end, Model: q.Model}
	snap, err := a.usageStore.QueryOverview(ctx, f, boundaries)
	if err != nil {
		return protocol.UsageOverview{}, localUsageQueryError(err)
	}
	status := "known"
	var collection *string
	if coverage.CollectionStartedAt != nil {
		s := coverage.CollectionStartedAt.UTC().Format(time.RFC3339Nano)
		collection = &s
	}
	if coverage.CollectionStartedAt == nil || ir.start.Before(*coverage.CollectionStartedAt) {
		status = "unknown"
	}
	gaps := make([]protocol.UsageCoverageGap, 0, len(coverage.Gaps))
	for _, gap := range coverage.Gaps {
		if !gap.StartAt.Before(ir.end) || !gap.EndAt.After(ir.start) {
			continue
		}
		start, end := gap.StartAt, gap.EndAt
		if start.Before(ir.start) {
			start = ir.start
		}
		if end.After(ir.end) {
			end = ir.end
		}
		gaps = append(gaps, protocol.UsageCoverageGap{StartAt: start.UTC().Format(time.RFC3339Nano), EndAt: end.UTC().Format(time.RFC3339Nano), Reason: gap.Reason})
	}
	if len(gaps) > 0 {
		status = "gap"
	}
	message := ""
	if !localUsageEnvEnabled() {
		coverage.StorageStatus, status = "degraded", "unknown"
		message = "本机采集已关闭，历史记录仍可查询"
	}
	trend := make([]protocol.UsageBucket, 0, len(snap.Buckets))
	for _, group := range snap.Buckets {
		totals := localUsageTotals(group.Totals)
		bucketStatus := "known"
		// A bucket with observed calls can show known samples even when its
		// earlier portion predates collection; empty earlier buckets stay unknown.
		if (coverage.CollectionStartedAt == nil || group.StartAt.Before(*coverage.CollectionStartedAt)) && group.Totals.RequestCount == 0 {
			bucketStatus = "unknown"
		}
		if !localUsageEnvEnabled() {
			bucketStatus = "unknown"
		}
		for _, gap := range coverage.Gaps {
			if gap.StartAt.Before(group.EndAt) && gap.EndAt.After(group.StartAt) {
				bucketStatus = "gap"
				break
			}
		}
		trend = append(trend, protocol.UsageBucket{
			StartAt: group.StartAt.UTC().Format(time.RFC3339Nano), EndAt: group.EndAt.UTC().Format(time.RFC3339Nano),
			RequestCount: totals.RequestCount, InputTokens: totals.InputTokens, OutputTokens: totals.OutputTokens, TotalTokens: totals.TotalTokens,
			CacheHitTokens: totals.CacheHitTokens, CacheEligibleInputTokens: totals.CacheEligibleInputTokens, Coverage: bucketStatus,
		})
	}
	totals := localUsageTotals(snap.Totals)
	return protocol.UsageOverview{
		ScopeID: f.ScopeID, SnapshotSeq: strconv.FormatInt(snap.Seq, 10), QueryKey: fmt.Sprintf("local:%d", snap.Seq), EffectiveRange: r,
		CollectionStartedAt: collection, CoverageStart: collection, CoverageGaps: gaps, CoverageStatus: status, CoverageMessage: message, StorageStatus: coverage.StorageStatus,
		StatusCounts: localUsageStatusCounts(snap.Totals.Statuses),
		DataAsOf:     now.UTC().Format(time.RFC3339Nano), Summary: totals, Trend: trend,
		Capabilities: protocol.UsageCapabilities{CacheMetrics: totals.CacheKnownRequestCount != "0", RequestStatus: true},
		Empty:        snap.Totals.RequestCount == 0, SourceKind: localusage.SourceKindLocalPlatform,
		Consistency: "local_snapshot", MappingVersion: localUsageMappingVersion,
	}, nil
}
func localUsageRow(c localusage.CallRecord) protocol.UsageRequestRow {
	started := c.StartedAt.UTC().Format(time.RFC3339Nano)
	recorded := started
	if c.FinishedAt != nil {
		recorded = c.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	row := protocol.UsageRequestRow{ID: c.CallID, RecordedAt: recorded, StartedAt: &started, Model: c.Model,
		MeterKind: "calls", DurationMs: c.DurationMs,
		Quality: protocol.UsageRowQuality{TokenSource: "unavailable", CacheSource: "unavailable", ReasoningSource: "unavailable"}}
	switch c.Status {
	case localusage.StatusPending, localusage.StatusSucceeded, localusage.StatusFailed, localusage.StatusCancelled, localusage.StatusSaveFailed:
		row.Status = c.Status
	default:
		row.Status = localusage.StatusUnknown
	}
	switch c.CallKind {
	case localusage.CallKindLLM, localusage.CallKindVision:
		row.MeterKind = "tokens"
		if (c.InputTokensKnown && c.InputTokens != nil) || (c.OutputTokensKnown && c.OutputTokens != nil) {
			row.Quality.TokenSource = "reported"
		}
		if c.CacheHitTokensKnown && c.CacheHitTokens != nil && c.CacheMissTokensKnown && c.CacheMissTokens != nil {
			h, m := strconv.FormatInt(*c.CacheHitTokens, 10), strconv.FormatInt(*c.CacheMissTokens, 10)
			row.CacheHitTokens, row.CacheMissTokens = &h, &m
			if c.CacheEstimated {
				row.Quality.CacheSource = "estimated"
			} else {
				row.Quality.CacheSource = "reported"
			}
		}
		if c.InputTokensKnown && c.InputTokens != nil {
			v := strconv.FormatInt(*c.InputTokens, 10)
			row.InputTokens = &v
		}
		if c.OutputTokensKnown && c.OutputTokens != nil {
			v := strconv.FormatInt(*c.OutputTokens, 10)
			row.OutputTokens = &v
		}
	case localusage.CallKindImage:
		row.MeterKind = "images"
		if c.GeneratedImages != nil {
			v := strconv.FormatInt(*c.GeneratedImages, 10)
			row.UsageQuantity = &v
			unit := "images"
			row.UsageUnit = &unit
		}
	}
	return row
}

func (a *App) localUsageRequests(q protocol.UsageRequestsQuery) (protocol.UsageRequestsPage, error) {
	if q.Query.ProviderID != "" {
		return protocol.UsageRequestsPage{}, usageError(protocol.ErrCodeUsageUnsupported, "本机记录暂不提供供应商筛选")
	}
	r, ir, err := localResolvedRange(q.Query)
	if err != nil {
		return protocol.UsageRequestsPage{}, err
	}
	f, err := a.localUsageFilter(q.Query.TenantID, q.Query.Model, ir)
	if err != nil {
		return protocol.UsageRequestsPage{}, err
	}
	if q.PageSize != 20 && q.PageSize != 50 && q.PageSize != 100 {
		return protocol.UsageRequestsPage{}, usageInvalidArgument("每页条数只能是 20、50 或 100")
	}
	if err := pinLocalUsageSnapshot(&f, q.Query.SnapshotSeq); err != nil {
		return protocol.UsageRequestsPage{}, err
	}
	page, err := a.usageStore.QueryPage(context.Background(), f, q.Cursor, q.PageSize)
	if err != nil {
		return protocol.UsageRequestsPage{}, localUsageQueryError(err)
	}
	items := make([]protocol.UsageRequestRow, 0, len(page.Calls))
	for _, c := range page.Calls {
		if c.Status == localusage.StatusPending {
			continue
		}
		items = append(items, localUsageRow(c))
	}
	return protocol.UsageRequestsPage{Items: items, TotalCount: strconv.Itoa(page.TotalCount), PageSize: q.PageSize,
		NextCursor: page.NextCursor, HasMore: page.HasMore, EffectiveRange: r, QueryKey: fmt.Sprintf("local:%d", page.Seq)}, nil
}

func (a *App) localUsageDimensions(q protocol.UsageDimensionsQuery) (protocol.UsageDimensionsPage, error) {
	if q.GroupBy != "model" {
		return protocol.UsageDimensionsPage{}, usageError(protocol.ErrCodeUsageUnsupported, "本机记录只支持按模型汇总")
	}
	if q.Query.ProviderID != "" {
		return protocol.UsageDimensionsPage{}, usageError(protocol.ErrCodeUsageUnsupported, "本机记录暂不提供供应商筛选")
	}
	r, ir, err := localResolvedRange(q.Query)
	if err != nil {
		return protocol.UsageDimensionsPage{}, err
	}
	f, err := a.localUsageFilter(q.Query.TenantID, q.Query.Model, ir)
	if err != nil {
		return protocol.UsageDimensionsPage{}, err
	}
	if q.Page < 1 || q.Page > usageMaxDimPage || (q.PageSize != 50 && q.PageSize != 100) || !validUsageText(q.Search, usageMaxSearchLen) {
		return protocol.UsageDimensionsPage{}, usageInvalidArgument("分组分页参数无效")
	}
	if err := pinLocalUsageSnapshot(&f, q.Query.SnapshotSeq); err != nil {
		return protocol.UsageDimensionsPage{}, err
	}
	page, err := a.usageStore.QueryDimensions(context.Background(), f, q.Search, q.Page, q.PageSize)
	if err != nil {
		return protocol.UsageDimensionsPage{}, localUsageQueryError(err)
	}
	items := make([]protocol.UsageDimensionRow, 0, len(page.Groups))
	for _, group := range page.Groups {
		items = append(items, protocol.UsageDimensionRow{ID: group.Model, Name: group.Model, Totals: localUsageTotals(group.Totals)})
	}
	return protocol.UsageDimensionsPage{Items: items, GroupBy: q.GroupBy, Page: q.Page, PageSize: q.PageSize,
		TotalCount: strconv.Itoa(page.TotalCount), HasMore: page.HasMore, EffectiveRange: r, QueryKey: fmt.Sprintf("local:%d", page.Seq)}, nil
}
