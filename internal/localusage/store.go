package localusage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动，打开 usage.sqlite3
)

// 写入预算：统计失败不得拖垮业务路径（设计：200ms）。
const writeBudget = 200 * time.Millisecond

const queryBudget = 5 * time.Second
const readPoolSize = 4
const usageRetention = 365 * 24 * time.Hour

var (
	// ErrSchemaTooNew 表示库 schema 比本二进制新，拒绝打开，不得删库。
	ErrSchemaTooNew = errors.New("本地用量库版本过新，当前客户端无法打开")
	// ErrSchemaUnsupported 表示遇到从未发布或已停止支持的 schema。
	ErrSchemaUnsupported = errors.New("本地用量库版本不受支持")
	// ErrSchemaCorrupt 表示 schema 元数据损坏或数据库无法识别。
	ErrSchemaCorrupt = errors.New("本地用量库 schema 已损坏")
	// ErrStoreClosed 表示关闭后拒绝写入。
	ErrStoreClosed = errors.New("本地用量记录器已关闭")
	// ErrInvalidRecord 表示字段不合法。
	ErrInvalidRecord = errors.New("本地用量记录字段不合法")
	// ErrScopeConflict 表示同一 callId 试图更换冻结 scope。
	ErrScopeConflict = errors.New("本地用量 callId 的归属 scope 不可更换")
)

// Store 是本机 usage.sqlite3 的唯一写入／查询入口；实现 Recorder。
type Store struct {
	db       *sql.DB
	readDB   *sql.DB
	path     string
	now      func() time.Time
	run      string
	coverage coverageMemory

	mu        sync.Mutex
	closed    bool
	active    sync.WaitGroup
	closeDone chan struct{}
	closeErr  error

	maintenanceDone chan struct{}
	maintenanceErr  error
}

// Open 打开或创建独立用量库。path 必须是绝对路径；now 可注入测试时钟。
// 损坏／版本过新／权限失败原样返回，不清空重建。
func Open(path string, now func() time.Time) (*Store, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: 路径必须是绝对路径", ErrInvalidRecord)
	}
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建用量目录失败: %w", err)
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath, RawQuery: "_pragma=busy_timeout(200)&_pragma=foreign_keys(1)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // 单写序列
	s := &Store{
		db: db, path: path, now: now, run: fmt.Sprintf("run-%d", now().UTC().UnixNano()),
		closeDone: make(chan struct{}), maintenanceDone: make(chan struct{}),
	}
	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	previousRun, err := s.activeRunMarker()
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.markRunDirty(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(queryIndexesSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	u.RawQuery = "mode=ro&_pragma=busy_timeout(200)&_pragma=query_only(1)"
	readDB, err := sql.Open("sqlite", u.String())
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	readDB.SetMaxOpenConns(readPoolSize)
	readDB.SetMaxIdleConns(readPoolSize)
	s.readDB = readDB
	s.startMaintenance(previousRun)
	return s, nil
}

// acquire protects the connection lifetime only. Database work never holds mu;
// the bounded writer connection queue is governed by the caller's context.
func (s *Store) acquire(ctx context.Context) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrStoreClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.active.Add(1)
	return s.active.Done, nil
}

func boundedQueryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, queryBudget)
}

func (s *Store) initSchema() error {
	var ver string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&ver)
	if errors.Is(err, sql.ErrNoRows) || isMissingTable(err) {
		if _, err := s.db.Exec(createSchemaSQL); err != nil {
			return fmt.Errorf("初始化用量 schema 失败: %w", err)
		}
		_, err = s.db.Exec(`INSERT INTO meta(key,value) VALUES('schema_version',?)`, fmt.Sprintf("%d", SchemaVersion))
		return err
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSchemaCorrupt, err)
	}
	var n int
	if _, err := fmt.Sscanf(ver, "%d", &n); err != nil {
		return fmt.Errorf("%w: schema_version=%q", ErrSchemaCorrupt, ver)
	}
	if n > SchemaVersion {
		return fmt.Errorf("%w: 库=%d 客户端=%d", ErrSchemaTooNew, n, SchemaVersion)
	}
	if n < SchemaVersion {
		if n == 2 {
			if err := migrateV2ToV3(s.db); err != nil {
				return fmt.Errorf("本地用量库迁移到 v3 失败: %w", err)
			}
			return nil
		}
		return fmt.Errorf("%w: 库=%d 当前=%d", ErrSchemaUnsupported, n, SchemaVersion)
	}
	return nil
}

// migrateV2ToV3 增加有效缓存物化表，并用窗口函数一次性回填历史 Token 调用。
// 迁移只在升级时执行，之后的查询与恢复不再展开逐行前序子查询。
func migrateV2ToV3(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS cache_estimates (
			call_id TEXT PRIMARY KEY NOT NULL,
			cache_hit_tokens INTEGER,
			cache_miss_tokens INTEGER,
			estimated INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT OR REPLACE INTO cache_estimates(call_id, cache_hit_tokens, cache_miss_tokens, estimated)
		WITH ordered AS (
			SELECT f.call_id,
			       f.input_tokens, f.input_tokens_known,
			       f.cache_hit_tokens, f.cache_hit_tokens_known,
			       f.cache_miss_tokens, f.cache_miss_tokens_known,
			       s.started_at_ms,
			       LAG(f.input_tokens) OVER (
			         PARTITION BY s.scope_id, s.model
			         ORDER BY s.started_at_ms, s.call_id
			       ) AS prev_input,
			       LAG(s.started_at_ms) OVER (
			         PARTITION BY s.scope_id, s.model
			         ORDER BY s.started_at_ms, s.call_id
			       ) AS prev_start
			FROM events s
			JOIN events f ON f.call_id=s.call_id AND f.phase='finish'
			WHERE s.phase='start' AND s.call_kind IN ('llm','vision')
		)
		SELECT call_id,
		       CASE
		         WHEN cache_hit_tokens_known<>0 AND cache_hit_tokens IS NOT NULL
		          AND cache_miss_tokens_known<>0 AND cache_miss_tokens IS NOT NULL
		           THEN cache_hit_tokens
		         WHEN input_tokens_known<>0 AND input_tokens IS NOT NULL
		          AND prev_input IS NOT NULL AND prev_start IS NOT NULL
		          AND started_at_ms-prev_start<=1800000 AND input_tokens>=prev_input
		           THEN MIN(input_tokens, prev_input)
		       END,
		       CASE
		         WHEN cache_hit_tokens_known<>0 AND cache_hit_tokens IS NOT NULL
		          AND cache_miss_tokens_known<>0 AND cache_miss_tokens IS NOT NULL
		           THEN cache_miss_tokens
		         WHEN input_tokens_known<>0 AND input_tokens IS NOT NULL
		          AND prev_input IS NOT NULL AND prev_start IS NOT NULL
		          AND started_at_ms-prev_start<=1800000 AND input_tokens>=prev_input
		           THEN input_tokens-prev_input
		       END,
		       CASE
		         WHEN cache_hit_tokens_known<>0 AND cache_hit_tokens IS NOT NULL
		          AND cache_miss_tokens_known<>0 AND cache_miss_tokens IS NOT NULL
		           THEN 0
		         WHEN input_tokens_known<>0 AND input_tokens IS NOT NULL
		          AND prev_input IS NOT NULL AND prev_start IS NOT NULL
		          AND started_at_ms-prev_start<=1800000 AND input_tokens>=prev_input
		           THEN 1
		         ELSE 0
		       END
		FROM ordered`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE meta SET value='3' WHERE key='schema_version'`); err != nil {
		return err
	}
	return tx.Commit()
}

func isMissingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

func (s *Store) markRunDirty() error {
	_, err := s.db.Exec(`INSERT INTO meta(key,value) VALUES('active_run_id',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, s.run)
	return err
}

func (s *Store) activeRunMarker() (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key='active_run_id'`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (s *Store) startMaintenance(previousRun string) {
	s.active.Add(1)
	go func() {
		defer s.active.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := s.recoverDirtyRuns(ctx, previousRun)
		if err == nil {
			_, err = s.pruneBefore(ctx, s.now().UTC().Add(-usageRetention))
		}
		s.mu.Lock()
		s.maintenanceErr = err
		close(s.maintenanceDone)
		s.mu.Unlock()
	}()
}

func (s *Store) awaitMaintenance(ctx context.Context) error {
	select {
	case <-s.maintenanceDone:
		s.mu.Lock()
		err := s.maintenanceErr
		s.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WaitMaintenance 等待启动恢复与保留清理结束；生产查询会自动等待，
// 该入口供启动状态检查和测试使用。
func (s *Store) WaitMaintenance(ctx context.Context) error {
	return s.awaitMaintenance(ctx)
}

// recoverDirtyRuns 在新 run 接纳调用前，把所有遗留 start 追加 unknown finish。
// 旧版 Close 可能误删 dirty marker，不能只依赖它找到未完成调用。
// 不在查询页面时恢复；不重发任何上游请求。
func (s *Store) recoverDirtyRuns(ctx context.Context, dirty string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT e.call_id, e.scope_id, e.account_digest, e.bridge_digest, e.tenant_id,
		       e.model, e.call_kind, e.session_id, e.turn_id, e.generation_id, e.started_at_ms, e.run_id
		FROM events e
		WHERE e.phase = 'start'
		  AND e.run_id <> ?
		  AND NOT EXISTS (SELECT 1 FROM events f WHERE f.call_id = e.call_id AND f.phase = 'finish')`, s.run)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	nowMs := s.now().UTC().UnixMilli()
	type pending struct {
		callID, scopeID, account, bridge, tenant, model, kind, session, turn, gen, runID string
		started                                                                          int64
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.callID, &p.scopeID, &p.account, &p.bridge, &p.tenant,
			&p.model, &p.kind, &p.session, &p.turn, &p.gen, &p.started, &p.runID); err != nil {
			return err
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}
	for _, p := range list {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO events(
				call_id, phase, run_id, scope_id, account_digest, bridge_digest, tenant_id,
				model, call_kind, session_id, turn_id, generation_id, started_at_ms,
				finished_at_ms, status, token_source, recorded_at_ms
			) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.callID, "finish", p.runID, p.scopeID, p.account, p.bridge, p.tenant,
			p.model, p.kind, p.session, p.turn, p.gen, p.started,
			nowMs, StatusUnknown, "unavailable", nowMs)
		if err != nil {
			return err
		}
		_, _ = tx.ExecContext(ctx, `
			INSERT INTO coverage_gaps(scope_id, start_at_ms, end_at_ms, reason)
			VALUES (?,?,?,?)`, p.scopeID, p.started, nowMs, "unclean_exit")
	}
	_, _ = tx.ExecContext(ctx, `INSERT INTO diagnostics(at_ms, code, detail) VALUES (?,?,?)`,
		nowMs, "recovered_unclean_exit", dirty)
	return tx.Commit()
}

// PruneBefore 删除 cutoff 之前开始的调用及其派生缓存估算。默认维护保留一年；
// 手动调用可用于测试或更严格的部署策略。
func (s *Store) PruneBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, queryBudget)
	defer cancel()
	if err := s.awaitMaintenance(ctx); err != nil {
		return 0, err
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer release()
	return s.pruneBefore(ctx, cutoff)
}

func (s *Store) pruneBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	cutoffMs := cutoff.UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM cache_estimates
		WHERE call_id IN (SELECT call_id FROM events WHERE phase='start' AND started_at_ms < ?)`,
		cutoffMs); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `
		DELETE FROM events
		WHERE call_id IN (SELECT call_id FROM events WHERE phase='start' AND started_at_ms < ?)`,
		cutoffMs)
	if err != nil {
		return 0, err
	}
	deleted, _ := res.RowsAffected()
	_, _ = tx.ExecContext(ctx, `DELETE FROM coverage_gaps WHERE end_at_ms < ?`, cutoffMs)
	_, _ = tx.ExecContext(ctx, `DELETE FROM diagnostics WHERE at_ms < ?`, cutoffMs)
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

// Begin 写入 start 事件；重复 start（同 callId）返回错误但不破坏库。
func (s *Store) Begin(ctx context.Context, start StartRecord) error {
	if err := validateStart(start); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, writeBudget)
	defer cancel()
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return s.beginLocked(ctx, start)
}

func (s *Store) beginLocked(ctx context.Context, start StartRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureScope(ctx, tx, start.Scope, start.StartedAt.UTC().UnixMilli()); err != nil {
		return err
	}
	// 已有同 callId 的 start：拒绝更换 scope；幂等重复 start 视为冲突（设计：UNIQUE）。
	var existingScope string
	err = tx.QueryRowContext(ctx, `SELECT scope_id FROM events WHERE call_id=? AND phase='start'`, start.CallID).Scan(&existingScope)
	if err == nil {
		if existingScope != start.Scope.ScopeID {
			return ErrScopeConflict
		}
		return fmt.Errorf("%w: callId 已有 start", ErrInvalidRecord)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	nowMs := s.now().UTC().UnixMilli()
	started := start.StartedAt.UTC().UnixMilli()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO events(
			call_id, phase, run_id, scope_id, account_digest, bridge_digest, tenant_id,
			model, call_kind, session_id, turn_id, generation_id, started_at_ms,
			status, token_source, recorded_at_ms
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		start.CallID, "start", s.run, start.Scope.ScopeID, start.Scope.AccountDigest, start.Scope.BridgeDigest, start.Scope.TenantID,
		start.Model, start.CallKind, start.SessionID, start.TurnID, start.GenerationID, started,
		StatusPending, "unavailable", nowMs)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Finish 写入终态。重复 finish 不覆盖先前终态；矛盾终态记诊断。
func (s *Store) Finish(ctx context.Context, finish FinishRecord) error {
	if err := validateFinish(finish); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, writeBudget)
	defer cancel()
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return s.finishLocked(ctx, finish)
}

func (s *Store) finishLocked(ctx context.Context, finish FinishRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var existingStatus sql.NullString
	var existingScope string
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(status,''), scope_id FROM events WHERE call_id=? AND phase='finish'`, finish.CallID).
		Scan(&existingStatus, &existingScope)
	if err == nil {
		// 已有终态：不覆盖；矛盾则记诊断。
		if existingStatus.String != "" && existingStatus.String != finish.Status {
			_, _ = tx.ExecContext(ctx, `INSERT INTO diagnostics(at_ms, code, detail) VALUES (?,?,?)`,
				s.now().UTC().UnixMilli(), "contradictory_finish",
				finish.CallID+":"+existingStatus.String+"->"+finish.Status)
			_ = tx.Commit()
			return nil // 不报业务失败，避免统计拖垮调用方
		}
		_ = tx.Commit()
		return nil // 幂等重复 finish
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	// 读取已有 start 的完整冻结元数据。finish 可能在调用方没有保留
	// FrozenStart 的情况下到达，不能因此把归属、模型或调用类型写空。
	var frozen StartRecord
	var startScope string
	var startedMs int64
	err = tx.QueryRowContext(ctx, `
		SELECT scope_id, account_digest, bridge_digest, tenant_id, model, call_kind,
		       session_id, turn_id, generation_id, started_at_ms
		FROM events WHERE call_id=? AND phase='start'`, finish.CallID).
		Scan(&startScope, &frozen.Scope.AccountDigest, &frozen.Scope.BridgeDigest,
			&frozen.Scope.TenantID, &frozen.Model, &frozen.CallKind,
			&frozen.SessionID, &frozen.TurnID, &frozen.GenerationID, &startedMs)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := validateStart(finish.FrozenStart); err != nil {
			return fmt.Errorf("补写 start 失败: %w", err)
		}
		if finish.FrozenStart.CallID != finish.CallID {
			return fmt.Errorf("%w: FrozenStart.callId 与 finish 不一致", ErrInvalidRecord)
		}
		if err := ensureScope(ctx, tx, finish.FrozenStart.Scope, finish.FrozenStart.StartedAt.UTC().UnixMilli()); err != nil {
			return err
		}
		frozen = finish.FrozenStart
		nowMs := s.now().UTC().UnixMilli()
		_, err = tx.ExecContext(ctx, `
			INSERT INTO events(
				call_id, phase, run_id, scope_id, account_digest, bridge_digest, tenant_id,
				model, call_kind, session_id, turn_id, generation_id, started_at_ms,
				status, token_source, recorded_at_ms
			) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			finish.FrozenStart.CallID, "start", s.run,
			finish.FrozenStart.Scope.ScopeID, finish.FrozenStart.Scope.AccountDigest,
			finish.FrozenStart.Scope.BridgeDigest, finish.FrozenStart.Scope.TenantID,
			finish.FrozenStart.Model, finish.FrozenStart.CallKind,
			finish.FrozenStart.SessionID, finish.FrozenStart.TurnID, finish.FrozenStart.GenerationID,
			finish.FrozenStart.StartedAt.UTC().UnixMilli(), StatusPending, "unavailable", nowMs)
		if err != nil {
			return err
		}
		startScope = finish.FrozenStart.Scope.ScopeID
	case err != nil:
		return err
	default:
		frozen.Scope.ScopeID = startScope
		frozen.StartedAt = time.UnixMilli(startedMs).UTC()
		if finish.FrozenStart.Scope.ScopeID != "" && finish.FrozenStart.Scope.ScopeID != startScope {
			return ErrScopeConflict
		}
	}

	nowMs := s.now().UTC().UnixMilli()
	finished := finish.FinishedAt.UTC().UnixMilli()
	started := frozen.StartedAt.UTC().UnixMilli()
	tokenSource := finish.TokenSource
	if tokenSource == "" {
		tokenSource = "unavailable"
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO events(
			call_id, phase, run_id, scope_id, account_digest, bridge_digest, tenant_id,
			model, call_kind, session_id, turn_id, generation_id, started_at_ms,
			finished_at_ms, duration_ms, status,
			input_tokens, input_tokens_known, output_tokens, output_tokens_known,
			cache_hit_tokens, cache_hit_tokens_known, cache_miss_tokens, cache_miss_tokens_known,
			generated_images, token_source, error_code, recorded_at_ms
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		finish.CallID, "finish", s.run, startScope,
		frozen.Scope.AccountDigest, frozen.Scope.BridgeDigest, frozen.Scope.TenantID,
		frozen.Model, frozen.CallKind,
		frozen.SessionID, frozen.TurnID, frozen.GenerationID,
		started, finished, finish.DurationMs, finish.Status,
		nullableInt(finish.InputTokensKnown, finish.InputTokens), boolInt(finish.InputTokensKnown),
		nullableInt(finish.OutputTokensKnown, finish.OutputTokens), boolInt(finish.OutputTokensKnown),
		nullableInt(finish.CacheHitTokensKnown, finish.CacheHitTokens), boolInt(finish.CacheHitTokensKnown),
		nullableInt(finish.CacheMissTokensKnown, finish.CacheMissTokens), boolInt(finish.CacheMissTokensKnown),
		finish.GeneratedImages, tokenSource, finish.ErrorCode, nowMs)
	if err != nil {
		return err
	}
	if err := materializeCacheEstimate(ctx, tx, finish, frozen); err != nil {
		return err
	}
	return tx.Commit()
}

func materializeCacheEstimate(ctx context.Context, tx *sql.Tx, finish FinishRecord, frozen StartRecord) error {
	if frozen.CallKind != CallKindLLM && frozen.CallKind != CallKindVision {
		return nil
	}
	var hit, miss sql.NullInt64
	estimated := 0
	reported := finish.CacheHitTokensKnown && finish.CacheHitTokens != nil &&
		finish.CacheMissTokensKnown && finish.CacheMissTokens != nil
	if reported {
		hit, miss = sql.NullInt64{Int64: *finish.CacheHitTokens, Valid: true}, sql.NullInt64{Int64: *finish.CacheMissTokens, Valid: true}
	} else if finish.InputTokensKnown && finish.InputTokens != nil && *finish.InputTokens >= 0 {
		var prevInput, prevStart int64
		started := frozen.StartedAt.UTC().UnixMilli()
		err := tx.QueryRowContext(ctx, `
			SELECT pf.input_tokens, ps.started_at_ms
			FROM events ps
			JOIN events pf ON pf.call_id=ps.call_id AND pf.phase='finish'
			WHERE ps.phase='start'
			  AND ps.scope_id=? AND ps.model=? AND ps.call_kind IN ('llm','vision')
			  AND pf.input_tokens_known<>0 AND pf.input_tokens IS NOT NULL
			  AND (ps.started_at_ms<? OR (ps.started_at_ms=? AND ps.call_id<?))
			ORDER BY ps.started_at_ms DESC, ps.call_id DESC
			LIMIT 1`,
			frozen.Scope.ScopeID, frozen.Model, started, started, finish.CallID).Scan(&prevInput, &prevStart)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			return err
		}
		if started-prevStart > estimateWindowMs || *finish.InputTokens < prevInput {
			return nil
		}
		h := min(*finish.InputTokens, prevInput)
		hit = sql.NullInt64{Int64: h, Valid: true}
		miss = sql.NullInt64{Int64: *finish.InputTokens - h, Valid: true}
		estimated = 1
	}
	if !hit.Valid || !miss.Valid {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO cache_estimates(call_id, cache_hit_tokens, cache_miss_tokens, estimated)
		VALUES(?,?,?,?)`, finish.CallID, hit.Int64, miss.Int64, estimated)
	return err
}

func ensureScope(ctx context.Context, tx *sql.Tx, scope Scope, startedMs int64) error {
	if scope.ScopeID == "" || scope.AccountDigest == "" || scope.BridgeDigest == "" || scope.TenantID == "" {
		return fmt.Errorf("%w: scope 不完整", ErrInvalidRecord)
	}
	var existing string
	err := tx.QueryRowContext(ctx, `SELECT scope_id FROM scopes WHERE scope_id=?`, scope.ScopeID).Scan(&existing)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO scopes(scope_id, account_digest, bridge_digest, tenant_id, collection_started_at_ms)
		VALUES (?,?,?,?,?)`,
		scope.ScopeID, scope.AccountDigest, scope.BridgeDigest, scope.TenantID, startedMs)
	return err
}

func nullableInt(known bool, v *int64) any {
	if !known || v == nil {
		return nil
	}
	return *v
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func validateStart(s StartRecord) error {
	if s.CallID == "" || s.Model == "" || s.CallKind == "" {
		return fmt.Errorf("%w: start 缺 callId/model/callKind", ErrInvalidRecord)
	}
	switch s.CallKind {
	case CallKindLLM, CallKindVision, CallKindImage, CallKindSearch:
	default:
		return fmt.Errorf("%w: 未知 callKind", ErrInvalidRecord)
	}
	if s.Scope.ScopeID == "" || s.Scope.TenantID == "" {
		return fmt.Errorf("%w: start scope 不完整", ErrInvalidRecord)
	}
	if s.StartedAt.IsZero() {
		return fmt.Errorf("%w: startedAt 必填", ErrInvalidRecord)
	}
	return nil
}

func validateFinish(f FinishRecord) error {
	if f.CallID == "" {
		return fmt.Errorf("%w: finish 缺 callId", ErrInvalidRecord)
	}
	switch f.Status {
	case StatusSucceeded, StatusFailed, StatusCancelled, StatusUnknown, StatusSaveFailed:
	default:
		return fmt.Errorf("%w: 非法 status %q", ErrInvalidRecord, f.Status)
	}
	if f.FinishedAt.IsZero() {
		return fmt.Errorf("%w: finishedAt 必填", ErrInvalidRecord)
	}
	return nil
}

// Close 释放连接。只有全部 start 已收尾才标记干净退出；否则保留 dirty
// marker，让下次启动将未完成调用恢复为 unknown，并记录覆盖缺口。
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		<-s.closeDone
		return s.closeErr
	}
	s.closed = true
	s.mu.Unlock()
	s.active.Wait()
	_, cleanErr := s.db.Exec(`DELETE FROM meta WHERE key = 'active_run_id'
		AND NOT EXISTS (
			SELECT 1 FROM events e WHERE e.phase = 'start'
			AND NOT EXISTS (SELECT 1 FROM events f WHERE f.call_id = e.call_id AND f.phase = 'finish')
		)`)
	s.closeErr = errors.Join(cleanErr, s.readDB.Close(), s.db.Close())
	close(s.closeDone)
	return s.closeErr
}

// Path 返回库文件绝对路径（测试／诊断用）。
func (s *Store) Path() string { return s.path }
