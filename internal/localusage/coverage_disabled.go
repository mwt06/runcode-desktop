package localusage

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

const captureDisabledSinceKey = "capture_disabled_since_ms"

// SetCaptureEnabled persists the desktop's capture state before it installs any
// providers. Disabled history has no provable account/tenant identity, so it
// records only a time interval that conservatively applies to every scope.
// The marker survives Close and abnormal exits until a later enabled startup.
func (s *Store) SetCaptureEnabled(ctx context.Context, enabled bool) error {
	ctx, cancel := boundedCoverageWriteContext(ctx, writeBudget)
	defer cancel()
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC().UnixMilli()
	if !enabled {
		// Repeated disabled starts retain the beginning of the unobserved span.
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO NOTHING`, captureDisabledSinceKey, strconv.FormatInt(now, 10)); err != nil {
			return err
		}
		return tx.Commit()
	}
	var since int64
	err = tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, captureDisabledSinceKey).Scan(&since)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if now <= since {
		now = since + 1
	}
	if _, err := persistCoverageEntry(ctx, tx, coverageEntry{Reason: "capture_disabled", StartMs: since, EndMs: now}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM meta WHERE key=?`, captureDisabledSinceKey); err != nil {
		return err
	}
	return tx.Commit()
}
