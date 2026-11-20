package localusage

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type StatusTotals struct {
	Pending, Succeeded, Failed, Cancelled, Unknown, SaveFailed int64
}

// AggregateTotals keeps known values and their sample counts separate. Pending
// calls contribute only to Statuses.Pending, never to requests or usage sums.
// Cache* 同时包含供应商上报与本地近似估算；CacheEstimatedCount 只计估算样本。
type AggregateTotals struct {
	RequestCount, TokenCallCount                                                    int64
	InputSampleCount, OutputSampleCount, TokenSampleCount, CompleteTokenSampleCount int64
	InputTokens, OutputTokens, TotalTokens                                          int64
	CacheSampleCount, CacheHitTokens, CacheMissTokens, CacheEligibleInputTokens     int64
	CacheEstimatedCount                                                             int64
	DurationSumMs, DurationSampleCount                                              int64
	Statuses                                                                        StatusTotals
}

type AggregateBucket struct {
	StartAt, EndAt time.Time
	Totals         AggregateTotals
}

type OverviewResult struct {
	Seq     int64
	Totals  AggregateTotals
	Buckets []AggregateBucket
}

type ModelAggregate struct {
	Model  string
	Totals AggregateTotals
}

type DimensionsResult struct {
	Seq        int64
	Groups     []ModelAggregate
	TotalCount int
	HasMore    bool
}

func (s *Store) QueryOverview(ctx context.Context, filter QueryFilter, boundaries []time.Time) (OverviewResult, error) {
	if err := validateQueryFilter(filter); err != nil {
		return OverviewResult{}, err
	}
	if len(boundaries) > 0 {
		if len(boundaries) < 2 || len(boundaries) > 601 || !boundaries[0].Equal(filter.StartAt) || !boundaries[len(boundaries)-1].Equal(filter.EndAt) {
			return OverviewResult{}, fmt.Errorf("趋势分桶范围无效或超过600桶")
		}
		for i := 1; i < len(boundaries); i++ {
			if boundaries[i].UnixMilli() <= boundaries[i-1].UnixMilli() {
				return OverviewResult{}, fmt.Errorf("趋势分桶范围无效")
			}
		}
	}
	ctx, cancel := boundedQueryContext(ctx)
	defer cancel()
	tx, seq, done, err := s.readSnapshot(ctx, filter)
	if err != nil {
		return OverviewResult{}, err
	}
	defer done()
	filter.SnapshotSeq = seq
	cols := aggregateColumnsSQL()
	out := OverviewResult{Seq: seq}
	if len(boundaries) == 0 {
		from, args := callsFrom(filter, true)
		err := tx.QueryRowContext(ctx, `SELECT `+cols+from, args...).Scan(out.Totals.scanArgs()...)
		if err != nil {
			return OverviewResult{}, err
		}
		out.Totals.finish()
		return out, nil
	}
	// Force the small boundary table to be the outer loop. Each start event is
	// visited once through its scope/time index, rather than once per bucket.
	var values []string
	args := make([]any, 0, len(boundaries)*3+5)
	out.Buckets = make([]AggregateBucket, len(boundaries)-1)
	for i := 0; i < len(boundaries)-1; i++ {
		values = append(values, "(?,?,?)")
		args = append(args, i, boundaries[i].UTC().UnixMilli(), boundaries[i+1].UTC().UnixMilli())
		out.Buckets[i] = AggregateBucket{StartAt: boundaries[i].UTC(), EndAt: boundaries[i+1].UTC()}
	}
	query := `WITH boundaries(bucket,begin_ms,end_ms) AS (VALUES ` + strings.Join(values, ",") + `)
SELECT b.bucket, ` + cols + `
FROM boundaries b CROSS JOIN events s
LEFT JOIN events f ON f.call_id=s.call_id AND f.phase='finish' AND f.seq<=?
LEFT JOIN cache_estimates ce ON ce.call_id=f.call_id
WHERE s.phase='start' AND s.seq<=? AND s.scope_id=? AND s.started_at_ms>=b.begin_ms AND s.started_at_ms<b.end_ms`
	args = append(args, seq, seq, filter.ScopeID)
	if filter.Model != "" {
		query += ` AND s.model=?`
		args = append(args, filter.Model)
	}
	query += ` GROUP BY b.bucket ORDER BY b.bucket`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return OverviewResult{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var index int
		var totals AggregateTotals
		if err := rows.Scan(append([]any{&index}, totals.scanArgs()...)...); err != nil {
			return OverviewResult{}, err
		}
		totals.finish()
		out.Buckets[index].Totals = totals
		out.Totals.add(totals)
	}
	if err := rows.Err(); err != nil {
		return OverviewResult{}, err
	}
	return out, nil
}

func (s *Store) QueryDimensions(ctx context.Context, filter QueryFilter, search string, page, pageSize int) (DimensionsResult, error) {
	if err := validateQueryFilter(filter); err != nil {
		return DimensionsResult{}, err
	}
	if page < 1 || page > 100000 || pageSize < 1 || pageSize > 1000 || len(search) > 1024 {
		return DimensionsResult{}, fmt.Errorf("分组分页参数无效")
	}
	ctx, cancel := boundedQueryContext(ctx)
	defer cancel()
	tx, seq, done, err := s.readSnapshot(ctx, filter)
	if err != nil {
		return DimensionsResult{}, err
	}
	defer done()
	filter.SnapshotSeq = seq
	cols := aggregateColumnsSQL()
	from, args := callsFrom(filter, false)
	if search != "" {
		from += ` AND instr(lower(s.model),lower(?))>0`
		args = append(args, search)
	}
	out := DimensionsResult{Seq: seq}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT s.model)`+from, args...).Scan(&out.TotalCount); err != nil {
		return DimensionsResult{}, err
	}
	offset := (page - 1) * pageSize
	args = append(args, pageSize, offset)
	rows, err := tx.QueryContext(ctx, `SELECT s.model, `+cols+from+` GROUP BY s.model ORDER BY COUNT(f.seq) DESC, s.model ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return DimensionsResult{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var group ModelAggregate
		if err := rows.Scan(append([]any{&group.Model}, group.Totals.scanArgs()...)...); err != nil {
			return DimensionsResult{}, err
		}
		group.Totals.finish()
		out.Groups = append(out.Groups, group)
	}
	if err := rows.Err(); err != nil {
		return DimensionsResult{}, err
	}
	out.HasMore = offset+len(out.Groups) < out.TotalCount
	return out, nil
}

const tokenCallSQL = `(s.call_kind IN ('llm','vision') AND f.seq IS NOT NULL)`
const inputKnownSQL = `(f.input_tokens_known<>0 AND f.input_tokens IS NOT NULL)`
const outputKnownSQL = `(f.output_tokens_known<>0 AND f.output_tokens IS NOT NULL)`
const reportedCacheSQL = `(` + tokenCallSQL + ` AND f.cache_hit_tokens_known<>0 AND f.cache_hit_tokens IS NOT NULL AND f.cache_miss_tokens_known<>0 AND f.cache_miss_tokens IS NOT NULL)`
const estimateWindowMs = int64(30 * 60 * 1000)

const effectiveCacheSQL = `(ce.cache_hit_tokens IS NOT NULL AND ce.cache_miss_tokens IS NOT NULL)`
const effectiveCacheHitSQL = `ce.cache_hit_tokens`
const effectiveCacheMissSQL = `ce.cache_miss_tokens`
const estimatedCacheSQL = `(ce.estimated<>0)`

func aggregateColumnsSQL() string {
	eff := effectiveCacheSQL
	hit := effectiveCacheHitSQL
	miss := effectiveCacheMissSQL
	est := estimatedCacheSQL
	return `COUNT(f.seq),
COUNT(CASE WHEN ` + tokenCallSQL + ` THEN 1 END),
COUNT(CASE WHEN ` + inputKnownSQL + ` THEN 1 END),
COUNT(CASE WHEN ` + outputKnownSQL + ` THEN 1 END),
COUNT(CASE WHEN ` + tokenCallSQL + ` AND (` + inputKnownSQL + ` OR ` + outputKnownSQL + `) THEN 1 END),
COUNT(CASE WHEN ` + tokenCallSQL + ` AND ` + inputKnownSQL + ` AND ` + outputKnownSQL + ` THEN 1 END),
COALESCE(SUM(CASE WHEN ` + inputKnownSQL + ` THEN f.input_tokens END),0),
COALESCE(SUM(CASE WHEN ` + outputKnownSQL + ` THEN f.output_tokens END),0),
COUNT(CASE WHEN ` + eff + ` THEN 1 END),
COALESCE(SUM(` + hit + `),0),
COALESCE(SUM(` + miss + `),0),
COUNT(CASE WHEN ` + est + ` THEN 1 END),
COALESCE(SUM(f.duration_ms),0), COUNT(f.duration_ms),
COUNT(CASE WHEN f.seq IS NULL THEN 1 END),
COUNT(CASE WHEN f.status='succeeded' THEN 1 END),
COUNT(CASE WHEN f.status='failed' THEN 1 END),
COUNT(CASE WHEN f.status='cancelled' THEN 1 END),
COUNT(CASE WHEN f.seq IS NOT NULL AND (f.status IS NULL OR f.status NOT IN ('succeeded','failed','cancelled','save_failed')) THEN 1 END),
COUNT(CASE WHEN f.status='save_failed' THEN 1 END)`
}

func (t *AggregateTotals) scanArgs() []any {
	return []any{&t.RequestCount, &t.TokenCallCount, &t.InputSampleCount, &t.OutputSampleCount, &t.TokenSampleCount, &t.CompleteTokenSampleCount, &t.InputTokens, &t.OutputTokens, &t.CacheSampleCount, &t.CacheHitTokens, &t.CacheMissTokens, &t.CacheEstimatedCount, &t.DurationSumMs, &t.DurationSampleCount, &t.Statuses.Pending, &t.Statuses.Succeeded, &t.Statuses.Failed, &t.Statuses.Cancelled, &t.Statuses.Unknown, &t.Statuses.SaveFailed}
}

func (t *AggregateTotals) finish() {
	t.TotalTokens = t.InputTokens + t.OutputTokens
	t.CacheEligibleInputTokens = t.CacheHitTokens + t.CacheMissTokens
}

func (t *AggregateTotals) add(other AggregateTotals) {
	t.RequestCount += other.RequestCount
	t.TokenCallCount += other.TokenCallCount
	t.InputSampleCount += other.InputSampleCount
	t.OutputSampleCount += other.OutputSampleCount
	t.TokenSampleCount += other.TokenSampleCount
	t.CompleteTokenSampleCount += other.CompleteTokenSampleCount
	t.InputTokens += other.InputTokens
	t.OutputTokens += other.OutputTokens
	t.CacheSampleCount += other.CacheSampleCount
	t.CacheHitTokens += other.CacheHitTokens
	t.CacheMissTokens += other.CacheMissTokens
	t.CacheEstimatedCount += other.CacheEstimatedCount
	t.DurationSumMs += other.DurationSumMs
	t.DurationSampleCount += other.DurationSampleCount
	t.Statuses.Pending += other.Statuses.Pending
	t.Statuses.Succeeded += other.Statuses.Succeeded
	t.Statuses.Failed += other.Statuses.Failed
	t.Statuses.Cancelled += other.Statuses.Cancelled
	t.Statuses.Unknown += other.Statuses.Unknown
	t.Statuses.SaveFailed += other.Statuses.SaveFailed
	t.finish()
}
