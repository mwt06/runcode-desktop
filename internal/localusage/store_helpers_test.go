package localusage

import (
	"context"
	"database/sql"
	"errors"
)

// CountCalls 返回某 scope 的唯一 start 数（测试用）。
func (s *Store) CountCalls(ctx context.Context, scopeID string) (int, error) {
	if err := s.WaitMaintenance(ctx); err != nil {
		return 0, err
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE scope_id=? AND phase='start'`, scopeID).Scan(&n)
	return n, err
}

// CallStatus 返回 callId 在 finish 水位的状态；无 finish 则看 start→pending。
func (s *Store) CallStatus(ctx context.Context, callID string) (string, error) {
	if err := s.WaitMaintenance(ctx); err != nil {
		return "", err
	}
	var st sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT status FROM events WHERE call_id=? AND phase='finish'`, callID).Scan(&st)
	if err == nil {
		return st.String, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var exists int
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM events WHERE call_id=? AND phase='start'`, callID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return "", sql.ErrNoRows
	}
	if err != nil {
		return "", err
	}
	return StatusPending, nil
}

// DiagnosticCount 返回诊断条数（测试用）。
func (s *Store) DiagnosticCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM diagnostics`).Scan(&n)
	return n, err
}
