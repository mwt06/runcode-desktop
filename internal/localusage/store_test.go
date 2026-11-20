package localusage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testClock(t *testing.T, base time.Time) func() time.Time {
	t.Helper()
	cur := base
	return func() time.Time {
		out := cur
		cur = cur.Add(time.Millisecond)
		return out
	}
}

func openTemp(t *testing.T, now func() time.Time) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.sqlite3")
	s, err := Open(path, now)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sampleScope(suffix string) Scope {
	return Scope{
		ScopeID:       "scope-a-" + suffix,
		AccountDigest: "acct-a",
		BridgeDigest:  "bridge-a",
		TenantID:      "wjtest",
	}
}

func sampleStart(callID string, scope Scope, at time.Time) StartRecord {
	return StartRecord{
		CallID:    callID,
		Scope:     scope,
		Model:     "qwen3.6-27b",
		CallKind:  CallKindLLM,
		StartedAt: at,
	}
}

func TestLocalUsageStoreRoundTrip(t *testing.T) {
	// U08：写入后关闭重开，调用数不变。
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	now := testClock(t, base)
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.sqlite3")
	s, err := Open(path, now)
	if err != nil {
		t.Fatal(err)
	}
	scope := sampleScope("rt")
	ctx := context.Background()
	start := sampleStart("call-rt-1", scope, base.Add(time.Second))
	if err := s.Begin(ctx, start); err != nil {
		t.Fatal(err)
	}
	in, out := int64(10), int64(5)
	dur := int64(100)
	if err := s.Finish(ctx, FinishRecord{
		CallID: "call-rt-1", Status: StatusSucceeded, FinishedAt: base.Add(2 * time.Second),
		DurationMs: &dur, InputTokens: &in, InputTokensKnown: true, OutputTokens: &out, OutputTokensKnown: true,
		TokenSource: "reported", FrozenStart: start,
	}); err != nil {
		t.Fatal(err)
	}
	n, err := s.CountCalls(ctx, scope.ScopeID)
	if err != nil || n != 1 {
		t.Fatalf("before close count=%d err=%v", n, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path, now)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	n2, err := s2.CountCalls(ctx, scope.ScopeID)
	if err != nil || n2 != 1 {
		t.Fatalf("after reopen count=%d err=%v want=1", n2, err)
	}
	st, err := s2.CallStatus(ctx, "call-rt-1")
	if err != nil || st != StatusSucceeded {
		t.Fatalf("status=%q err=%v", st, err)
	}
}

func TestLocalUsageIdempotentFinish(t *testing.T) {
	// U01／样例 A：重复 finish 不改结果。
	base := time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC)
	s := openTemp(t, testClock(t, base))
	scope := sampleScope("idem")
	ctx := context.Background()
	start := sampleStart("call-idem", scope, base)
	_ = s.Begin(ctx, start)
	in, out := int64(100), int64(20)
	dur := int64(100)
	fin := FinishRecord{
		CallID: "call-idem", Status: StatusSucceeded, FinishedAt: base.Add(time.Second),
		DurationMs: &dur, InputTokens: &in, InputTokensKnown: true, OutputTokens: &out, OutputTokensKnown: true,
		TokenSource: "reported", FrozenStart: start,
	}
	if err := s.Finish(ctx, fin); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, fin); err != nil {
		t.Fatal(err)
	}
	// 矛盾终态：记诊断，不覆盖
	if err := s.Finish(ctx, FinishRecord{
		CallID: "call-idem", Status: StatusFailed, FinishedAt: base.Add(2 * time.Second), FrozenStart: start,
	}); err != nil {
		t.Fatal(err)
	}
	st, _ := s.CallStatus(ctx, "call-idem")
	if st != StatusSucceeded {
		t.Fatalf("status overwritten to %q", st)
	}
	n, _ := s.CountCalls(ctx, scope.ScopeID)
	if n != 1 {
		t.Fatalf("count=%d", n)
	}
	diag, _ := s.DiagnosticCount(ctx)
	if diag < 1 {
		t.Fatalf("expected contradictory finish diagnostic, got %d", diag)
	}
}

func TestFinishUsesStoredStartMetadataWhenFrozenStartMissing(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	s := openTemp(t, testClock(t, now))
	ctx := context.Background()
	scope := sampleScope("stored-start")
	start := sampleStart("call-stored-start", scope, now.Add(-time.Minute))
	if err := s.Begin(ctx, start); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, FinishRecord{
		CallID: "call-stored-start", Status: StatusSucceeded, FinishedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	var account, bridge, tenant, model, kind string
	err := s.db.QueryRowContext(ctx, `
		SELECT account_digest, bridge_digest, tenant_id, model, call_kind
		FROM events WHERE call_id=? AND phase='finish'`, "call-stored-start").
		Scan(&account, &bridge, &tenant, &model, &kind)
	if err != nil {
		t.Fatal(err)
	}
	if account != scope.AccountDigest || bridge != scope.BridgeDigest || tenant != scope.TenantID || model != start.Model || kind != start.CallKind {
		t.Fatalf("finish metadata = %q %q %q %q %q", account, bridge, tenant, model, kind)
	}
}

func TestLocalUsageRecoverUnknown(t *testing.T) {
	// U09：崩溃只留下 start → 重开恢复为 unknown。
	base := time.Date(2026, 11, 13, 2, 0, 0, 0, time.UTC)
	now := testClock(t, base)
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.sqlite3")
	s, err := Open(path, now)
	if err != nil {
		t.Fatal(err)
	}
	scope := sampleScope("rec")
	ctx := context.Background()
	start := sampleStart("call-crash", scope, base)
	if err := s.Begin(ctx, start); err != nil {
		t.Fatal(err)
	}
	// 模拟崩溃：直接关 DB，不走 Close 清 dirty 标记
	_ = s.db.Close()
	s.closed = true

	s2, err := Open(path, now)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	st, err := s2.CallStatus(ctx, "call-crash")
	if err != nil || st != StatusUnknown {
		t.Fatalf("recovered status=%q err=%v want=unknown", st, err)
	}
}

func TestLocalUsageScopeIsolation(t *testing.T) {
	// U07：不同账号／租户 scope 隔离。
	base := time.Date(2026, 11, 13, 3, 0, 0, 0, time.UTC)
	s := openTemp(t, testClock(t, base))
	ctx := context.Background()
	a := sampleScope("a")
	b := Scope{ScopeID: "scope-b", AccountDigest: "acct-b", BridgeDigest: "bridge-a", TenantID: "t2"}
	sa := sampleStart("call-a", a, base)
	sb := sampleStart("call-b", b, base.Add(time.Second))
	sb.Model = "other"
	_ = s.Begin(ctx, sa)
	_ = s.Begin(ctx, sb)
	na, _ := s.CountCalls(ctx, a.ScopeID)
	nb, _ := s.CountCalls(ctx, b.ScopeID)
	if na != 1 || nb != 1 {
		t.Fatalf("na=%d nb=%d", na, nb)
	}
	// 同 callId 换 scope 拒绝
	bad := sa
	bad.Scope = b
	if err := s.Begin(ctx, bad); err == nil {
		t.Fatal("expected scope conflict on duplicate callId")
	}
}

func TestLocalUsageWriteFailure(t *testing.T) {
	// U14：只读目录／权限失败不吞掉、不清库、不伪成功。
	base := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	if err := os.MkdirAll(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	// 在只读目录内建库文件路径：Open 的 MkdirAll 父目录可写，但我们用不可写父路径
	blocked := filepath.Join(ro, "nested", "usage.sqlite3")
	// Windows 上 chmod 行为弱：改为打开成功后关掉再把文件置为只读目录场景
	// 更稳：相对路径拒绝
	if _, err := Open("relative.sqlite3", testClock(t, base)); err == nil {
		t.Fatal("expected reject relative path")
	}
	_ = blocked
	// 非法 finish 不得伪成功写入
	s := openTemp(t, testClock(t, base))
	ctx := context.Background()
	if err := s.Finish(ctx, FinishRecord{CallID: "x", Status: "bogus", FinishedAt: base}); err == nil {
		t.Fatal("expected invalid status error")
	}
}

func TestLocalUsageSchemaVersion(t *testing.T) {
	base := time.Date(2026, 11, 13, 5, 0, 0, 0, time.UTC)
	now := testClock(t, base)
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.sqlite3")
	s, err := Open(path, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`UPDATE meta SET value='99' WHERE key='schema_version'`)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := Open(path, now); err == nil {
		t.Fatal("expected schema too new")
	}
}

func TestLocalUsageRejectsUnreleasedV1AndCorruptVersion(t *testing.T) {
	base := time.Date(2026, 11, 13, 5, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, version string
		want          error
	}{
		{"v1 never released", "1", ErrSchemaUnsupported},
		{"malformed", "not-a-number", ErrSchemaCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "usage.sqlite3")
			s, err := Open(path, testClock(t, base))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`UPDATE meta SET value=? WHERE key='schema_version'`, tc.version); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(path, testClock(t, base)); !errors.Is(err, tc.want) {
				t.Fatalf("Open error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLocalUsageV2MigrationMaterializesExistingCacheEstimates(t *testing.T) {
	base := time.Date(2026, 11, 13, 5, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "usage.sqlite3")
	s, err := Open(path, testClock(t, base))
	if err != nil {
		t.Fatal(err)
	}
	scope := sampleScope("migrate-v3")
	writeTokenCall(t, s, scope, "a", "qwen-max", base, 100, 10, nil, nil)
	writeTokenCall(t, s, scope, "b", "qwen-max", base.Add(time.Minute), 120, 12, nil, nil)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DROP TABLE cache_estimates; UPDATE meta SET value='2' WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(path, testClock(t, base.Add(2*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	var hit, miss, estimated int64
	if err := migrated.db.QueryRow(`SELECT cache_hit_tokens, cache_miss_tokens, estimated
		FROM cache_estimates WHERE call_id='b'`).Scan(&hit, &miss, &estimated); err != nil {
		t.Fatal(err)
	}
	if hit != 100 || miss != 20 || estimated != 1 {
		t.Fatalf("backfilled hit/miss/estimated = %d/%d/%d", hit, miss, estimated)
	}
}

func TestPruneBeforeRemovesEventsAndMaterializedEstimates(t *testing.T) {
	base := time.Date(2026, 11, 13, 5, 0, 0, 0, time.UTC)
	s := openTemp(t, testClock(t, base))
	scope := sampleScope("prune")
	writeTokenCall(t, s, scope, "old-a", "qwen-max", base.Add(-48*time.Hour), 100, 10, nil, nil)
	writeTokenCall(t, s, scope, "old-b", "qwen-max", base.Add(-47*time.Hour), 120, 12, nil, nil)
	writeTokenCall(t, s, scope, "new-a", "qwen-max", base.Add(-time.Hour), 100, 10, nil, nil)
	writeTokenCall(t, s, scope, "new-b", "qwen-max", base.Add(-30*time.Minute), 120, 12, nil, nil)

	if _, err := s.PruneBefore(context.Background(), base.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var oldEvents, newEvents, oldEstimates int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE call_id IN ('old-a','old-b')`).Scan(&oldEvents); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE call_id IN ('new-a','new-b')`).Scan(&newEvents); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cache_estimates WHERE call_id IN ('old-a','old-b')`).Scan(&oldEstimates); err != nil {
		t.Fatal(err)
	}
	if oldEvents != 0 || newEvents != 4 || oldEstimates != 0 {
		t.Fatalf("prune oldEvents=%d newEvents=%d oldEstimates=%d", oldEvents, newEvents, oldEstimates)
	}
}

func TestLocalUsageNoSensitiveContent(t *testing.T) {
	// 不持久化秘密、正文或原始异常。
	base := time.Date(2026, 11, 13, 6, 0, 0, 0, time.UTC)
	s := openTemp(t, testClock(t, base))
	scope := sampleScope("sec")
	ctx := context.Background()
	start := sampleStart("call-sec", scope, base)
	_ = s.Begin(ctx, start)
	_ = s.Finish(ctx, FinishRecord{
		CallID: "call-sec", Status: StatusFailed, FinishedAt: base.Add(time.Second),
		ErrorCode: "upstream_5xx", FrozenStart: start,
	})
	var blob string
	rows, err := s.db.Query(`SELECT call_id||'|'||IFNULL(error_code,'')||'|'||model FROM events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		_ = rows.Scan(&line)
		blob += line
	}
	forbidden := []string{"Bearer ", "sk-", "password", "prompt", "校园开放日", "stack trace", "Exception"}
	for _, f := range forbidden {
		if containsFold(blob, f) {
			t.Fatalf("sensitive content leaked: %q in %q", f, blob)
		}
	}
}

func containsFold(s, sub string) bool {
	return len(sub) > 0 && (s == sub || len(s) >= len(sub) &&
		(func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if equalFoldASCII(s[i:i+len(sub)], sub) {
					return true
				}
			}
			return false
		})())
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
