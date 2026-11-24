package localusage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// QueryFilter 是本机查询的最小条件。ScopeID 已由宿主根据当前账号、Bridge 和租户冻结，
// 查询层不接受自由账号字段，也不会访问在线服务补齐身份。
type QueryFilter struct {
	ScopeID        string
	StartAt        time.Time
	EndAt          time.Time
	Model          string
	SnapshotSeq    int64
	SnapshotPinned bool // distinguishes a pinned empty snapshot (0) from a fresh query
}

// CallRecord 是从 start 与 finish 事件还原的一次逻辑调用；同一 callId 永远只返回一行。
type CallRecord struct {
	CallID               string
	ScopeID              string
	Model                string
	CallKind             string
	Status               string
	StartedAt            time.Time
	FinishedAt           *time.Time
	DurationMs           *int64
	InputTokens          *int64
	InputTokensKnown     bool
	OutputTokens         *int64
	OutputTokensKnown    bool
	CacheHitTokens       *int64
	CacheHitTokensKnown  bool
	CacheMissTokens      *int64
	CacheMissTokensKnown bool
	CacheEstimated       bool // 有效缓存来自本地近似，而非供应商上报
	GeneratedImages      *int64
}

// QueryPage 是分页查询结果。
type QueryPage struct {
	Seq        int64
	Calls      []CallRecord
	TotalCount int
	NextCursor string
	HasMore    bool
}

type queryCursor struct {
	Seq        int64  `json:"seq"`
	Started    int64  `json:"started"`
	CallID     string `json:"callId"`
	ScopeID    string `json:"scopeId"`
	Model      string `json:"model"`
	StartRange int64  `json:"startRange"`
	EndRange   int64  `json:"endRange"`
}

var (
	ErrInvalidCursor   = errors.New("分页游标无效")
	ErrInvalidSnapshot = errors.New("查询快照已失效")
)

// readSnapshot pins all statements to one SQLite read transaction and event seq.
func (s *Store) readSnapshot(ctx context.Context, filter QueryFilter) (*sql.Tx, int64, func(), error) {
	ctx, cancel := boundedQueryContext(ctx)
	if err := s.awaitMaintenance(ctx); err != nil {
		cancel()
		return nil, 0, nil, err
	}
	release, err := s.acquire(ctx)
	if err != nil {
		cancel()
		return nil, 0, nil, err
	}
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		release()
		cancel()
		return nil, 0, nil, err
	}
	cleanup := func() { _ = tx.Rollback(); release(); cancel() }
	seq, err := snapshotSeq(ctx, tx, filter.SnapshotSeq, filter.SnapshotPinned)
	if err != nil {
		cleanup()
		return nil, 0, nil, err
	}
	return tx, seq, cleanup, nil
}

// QueryPage uses a SQL keyset and LIMIT; late finishes remain outside its seq.
func (s *Store) QueryPage(ctx context.Context, filter QueryFilter, cursor string, limit int) (QueryPage, error) {
	if limit <= 0 || limit > 1000 {
		return QueryPage{}, fmt.Errorf("查询页大小无效")
	}
	if err := validateQueryFilter(filter); err != nil {
		return QueryPage{}, err
	}
	var decoded queryCursor
	if cursor != "" {
		var err error
		decoded, err = decodeCursor(cursor)
		if err != nil || decoded.ScopeID != filter.ScopeID || decoded.Model != filter.Model || decoded.StartRange != filter.StartAt.UTC().UnixMilli() || decoded.EndRange != filter.EndAt.UTC().UnixMilli() || ((filter.SnapshotPinned || filter.SnapshotSeq > 0) && filter.SnapshotSeq != decoded.Seq) {
			return QueryPage{}, ErrInvalidCursor
		}
		filter.SnapshotSeq, filter.SnapshotPinned = decoded.Seq, true
	}
	ctx, cancel := boundedQueryContext(ctx)
	defer cancel()
	tx, seq, done, err := s.readSnapshot(ctx, filter)
	if err != nil {
		return QueryPage{}, err
	}
	defer done()
	filter.SnapshotSeq = seq
	from, args := callsFrom(filter, false)
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		return QueryPage{}, err
	}
	if cursor != "" {
		var exists int
		checkArgs := append(append([]any{}, args...), decoded.Started, decoded.CallID)
		err := tx.QueryRowContext(ctx, `SELECT 1`+from+` AND s.started_at_ms=? AND s.call_id=?`, checkArgs...).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return QueryPage{}, ErrInvalidCursor
		}
		if err != nil {
			return QueryPage{}, err
		}
		from += ` AND (s.started_at_ms, s.call_id) < (?, ?)`
		args = append(args, decoded.Started, decoded.CallID)
	}
	args = append(args, limit+1)
	calls, err := queryCalls(ctx, tx, callColumnsSQL()+from+` ORDER BY s.started_at_ms DESC, s.call_id DESC LIMIT ?`, args)
	if err != nil {
		return QueryPage{}, err
	}
	out := QueryPage{Seq: seq, Calls: calls, TotalCount: total, HasMore: len(calls) > limit}
	if out.HasMore {
		out.Calls = calls[:limit]
		last := out.Calls[len(out.Calls)-1]
		out.NextCursor = encodeCursor(queryCursor{Seq: seq, Started: last.StartedAt.UnixMilli(), CallID: last.CallID, ScopeID: filter.ScopeID, Model: filter.Model, StartRange: filter.StartAt.UTC().UnixMilli(), EndRange: filter.EndAt.UTC().UnixMilli()})
	}
	return out, nil
}

// ListTenantIDs lists the current account's historical tenants.
func (s *Store) ListTenantIDs(ctx context.Context, accountDigest, bridgeDigest string) ([]string, error) {
	if accountDigest == "" || bridgeDigest == "" {
		return nil, fmt.Errorf("账号或 Bridge 摘要不能为空")
	}
	ctx, cancel := boundedQueryContext(ctx)
	defer cancel()
	if err := s.awaitMaintenance(ctx); err != nil {
		return nil, err
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := s.readDB.QueryContext(ctx, `SELECT DISTINCT tenant_id FROM scopes WHERE account_digest=? AND bridge_digest=? AND tenant_id<>'' ORDER BY tenant_id`, accountDigest, bridgeDigest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var tenant string
		if err := rows.Scan(&tenant); err != nil {
			return nil, err
		}
		out = append(out, tenant)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func validateQueryFilter(f QueryFilter) error {
	if f.ScopeID == "" || f.StartAt.IsZero() || f.EndAt.IsZero() || !f.StartAt.Before(f.EndAt) || f.StartAt.Location() == nil || f.EndAt.Location() == nil {
		return errors.New("查询范围无效")
	}
	return nil
}

func snapshotSeq(ctx context.Context, tx *sql.Tx, requested int64, pinned bool) (int64, error) {
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM events`).Scan(&current); err != nil {
		return 0, err
	}
	if requested < 0 || requested > current {
		return 0, ErrInvalidSnapshot
	}
	if requested > 0 || pinned {
		return requested, nil
	}
	return current, nil
}

func callColumnsSQL() string {
	return `SELECT s.call_id, s.scope_id, s.model, s.call_kind, s.started_at_ms,
 f.finished_at_ms, f.duration_ms, CASE WHEN f.seq IS NULL THEN 'pending'
 WHEN f.status IN ('succeeded','failed','cancelled','unknown','save_failed') THEN f.status ELSE 'unknown' END,
 f.input_tokens, f.input_tokens_known, f.output_tokens, f.output_tokens_known,
 ` + effectiveCacheHitSQL + `, ` + effectiveCacheMissSQL + `,
 CASE WHEN ` + estimatedCacheSQL + ` THEN 1 ELSE 0 END,
 f.generated_images`
}

func callsFrom(f QueryFilter, includePending bool) (string, []any) {
	join := ` JOIN `
	if includePending {
		join = ` LEFT JOIN `
	}
	query := ` FROM events s` + join + `events f ON f.call_id=s.call_id AND f.phase='finish' AND f.seq<=?
	 LEFT JOIN cache_estimates ce ON ce.call_id=f.call_id
 WHERE s.phase='start' AND s.seq<=? AND s.scope_id=? AND s.started_at_ms>=? AND s.started_at_ms<?`
	args := []any{f.SnapshotSeq, f.SnapshotSeq, f.ScopeID, f.StartAt.UTC().UnixMilli(), f.EndAt.UTC().UnixMilli()}
	if f.Model != "" {
		query += ` AND s.model=?`
		args = append(args, f.Model)
	}
	return query, args
}

func queryCalls(ctx context.Context, tx *sql.Tx, query string, args []any) ([]CallRecord, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CallRecord
	for rows.Next() {
		var call CallRecord
		var started, finished sql.NullInt64
		var duration, input, inputKnown, output, outputKnown, cacheHit, cacheMiss, estimated, images sql.NullInt64
		if err := rows.Scan(&call.CallID, &call.ScopeID, &call.Model, &call.CallKind, &started, &finished, &duration, &call.Status, &input, &inputKnown, &output, &outputKnown, &cacheHit, &cacheMiss, &estimated, &images); err != nil {
			return nil, err
		}
		call.StartedAt = time.UnixMilli(started.Int64).UTC()
		if finished.Valid {
			t := time.UnixMilli(finished.Int64).UTC()
			call.FinishedAt = &t
		}
		if duration.Valid {
			v := duration.Int64
			call.DurationMs = &v
		}
		call.InputTokensKnown = inputKnown.Valid && inputKnown.Int64 != 0 && input.Valid
		if call.InputTokensKnown {
			v := input.Int64
			call.InputTokens = &v
		}
		call.OutputTokensKnown = outputKnown.Valid && outputKnown.Int64 != 0 && output.Valid
		if call.OutputTokensKnown {
			v := output.Int64
			call.OutputTokens = &v
		}
		// 有效缓存（上报或估算）直接由 SQL 产出；已知标记与两侧同时有值对齐。
		if cacheHit.Valid && cacheMiss.Valid {
			h, m := cacheHit.Int64, cacheMiss.Int64
			call.CacheHitTokens, call.CacheMissTokens = &h, &m
			call.CacheHitTokensKnown, call.CacheMissTokensKnown = true, true
			call.CacheEstimated = estimated.Valid && estimated.Int64 != 0
		}
		if images.Valid {
			v := images.Int64
			call.GeneratedImages = &v
		}
		out = append(out, call)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func encodeCursor(c queryCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (queryCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return queryCursor{}, err
	}
	var c queryCursor
	if err := json.Unmarshal(b, &c); err != nil || c.Seq <= 0 || c.Started <= 0 || c.CallID == "" || c.ScopeID == "" || c.StartRange < 0 || c.EndRange <= c.StartRange {
		return queryCursor{}, errors.New("游标内容无效")
	}
	return c, nil
}
