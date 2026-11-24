package localusage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Coverage metadata never assigns an unattributed call to a statistical scope.
// Unknown identity fields conservatively match any value in that dimension.
type CoverageGap struct {
	StartAt time.Time
	EndAt   time.Time
	Reason  string
}

type CoverageMetadata struct {
	CollectionStartedAt *time.Time
	Gaps                []CoverageGap
	StorageStatus       string // ok / degraded
	CoverageStatus      string // known / gap / unknown
}

type CoverageDiagnostic struct {
	At            time.Time
	ScopeID       string
	AccountDigest string
	BridgeDigest  string
	TenantID      string // Optional input; only its digest is persisted.
	Reason        string
}

type coverageEntry struct {
	ScopeID       string `json:"scopeId,omitempty"`
	AccountDigest string `json:"accountDigest,omitempty"`
	BridgeDigest  string `json:"bridgeDigest,omitempty"`
	TenantDigest  string `json:"tenantDigest,omitempty"`
	Reason        string `json:"reason"`
	StartMs       int64  `json:"startMs"`
	EndMs         int64  `json:"endMs"`
}

type coverageMemory struct {
	mu      sync.Mutex
	pending map[string]coverageEntry
}

const (
	coverageDiagnosticPrefix = "coverage_gap:"
	coverageDiagnosticLimit  = 256
	// Coverage reporting shares the call's remaining write budget. A diagnostic
	// never adds another full 200 ms wait after a failed Begin or Finish.
	coverageWriteBudget = 10 * time.Millisecond
)

// Select the effective deadline explicitly. WithTimeout can inherit an earlier
// parent's cancellation signal even after that deadline has passed, while its
// timer callback is still waiting to run. WithDeadline at that exact deadline
// synchronously cancels an already expired budget before any SQL work starts.
func boundedCoverageWriteContext(ctx context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(budget)
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	return context.WithDeadline(ctx, deadline)
}

func validCoverageReason(reason string) bool {
	switch reason {
	case "invalid_platform_scope", "missing_stable_identity", "begin_failed", "finish_failed", "coverage_history_compacted", "unclean_exit", "collection_gap", "capture_disabled":
		return true
	default:
		return false
	}
}

func validCoverageDigest(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != 32 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func coverageTenantDigest(tenant string) string {
	if tenant == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(tenant))
	return hex.EncodeToString(sum[:16])
}

func coverageKey(entry coverageEntry) string {
	identity := entry.ScopeID + "\x00" + entry.AccountDigest + "\x00" + entry.BridgeDigest + "\x00" + entry.TenantDigest + "\x00" + entry.Reason
	if entry.Reason == "capture_disabled" {
		// Distinct disabled intervals must not swallow healthy enabled time.
		identity += "\x00" + strconv.FormatInt(entry.StartMs, 10)
	}
	sum := sha256.Sum256([]byte(identity))
	return coverageDiagnosticPrefix + hex.EncodeToString(sum[:16])
}

func mergeCoverageEntry(a, b coverageEntry) coverageEntry {
	if b.StartMs < a.StartMs {
		a.StartMs = b.StartMs
	}
	if b.EndMs > a.EndMs {
		a.EndMs = b.EndMs
	}
	return a
}

func compactCoverageEntry(entry coverageEntry) coverageEntry {
	return coverageEntry{Reason: "coverage_history_compacted", StartMs: entry.StartMs, EndMs: entry.EndMs}
}

func (m *coverageMemory) add(entry coverageEntry) coverageEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		m.pending = make(map[string]coverageEntry)
	}
	key := coverageKey(entry)
	if old, ok := m.pending[key]; ok {
		m.pending[key] = mergeCoverageEntry(old, entry)
		return m.pending[key]
	}
	if len(m.pending) >= coverageDiagnosticLimit-1 {
		entry = compactCoverageEntry(entry)
		key = coverageKey(entry)
		if old, ok := m.pending[key]; ok {
			entry = mergeCoverageEntry(old, entry)
		}
	}
	m.pending[key] = entry
	return entry
}

func (m *coverageMemory) snapshot() []coverageEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := make([]coverageEntry, 0, len(m.pending))
	for _, entry := range m.pending {
		entries = append(entries, entry)
	}
	return entries
}

func (m *coverageMemory) persisted(entry coverageEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := coverageKey(entry)
	if pending, ok := m.pending[key]; ok && pending.StartMs >= entry.StartMs && pending.EndMs <= entry.EndMs {
		delete(m.pending, key)
	}
}

// RecordCoverageGap records only allowlisted reasons and optional identity
// digests. It cannot store URLs, tokens, error text, tenant names or call IDs.
// Each identity/reason pair keeps a conservative first-to-last interval; a
// bounded overflow interval explicitly says that attribution was compacted.
// If SQLite is unavailable, the in-memory fallback still exposes the failure.
func (s *Store) RecordCoverageGap(ctx context.Context, diagnostic CoverageDiagnostic) error {
	if !validCoverageReason(diagnostic.Reason) || !validCoverageDigest(diagnostic.ScopeID) || !validCoverageDigest(diagnostic.AccountDigest) || !validCoverageDigest(diagnostic.BridgeDigest) {
		return fmt.Errorf("%w: unsafe coverage diagnostic", ErrInvalidRecord)
	}
	if diagnostic.At.IsZero() {
		diagnostic.At = time.Now()
	}
	entry := coverageEntry{
		ScopeID: diagnostic.ScopeID, AccountDigest: diagnostic.AccountDigest, BridgeDigest: diagnostic.BridgeDigest,
		TenantDigest: coverageTenantDigest(diagnostic.TenantID),
		Reason:       diagnostic.Reason, StartMs: diagnostic.At.UTC().UnixMilli(), EndMs: diagnostic.At.UTC().UnixMilli() + 1,
	}
	entry = s.coverage.add(entry)
	pendingEntry := entry
	ctx, cancel := boundedCoverageWriteContext(ctx, coverageWriteBudget)
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
	entry, err = persistCoverageEntry(ctx, tx, entry)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.coverage.persisted(pendingEntry)
	s.coverage.persisted(entry)
	return nil
}

// persistCoverageEntry shares one bounded diagnostic budget across scope gaps
// and disabled intervals. At capacity, unknown attribution is conservative.
func persistCoverageEntry(ctx context.Context, tx *sql.Tx, entry coverageEntry) (coverageEntry, error) {
	key := coverageKey(entry)
	var seq int64
	var detail string
	err := tx.QueryRowContext(ctx, `SELECT seq, detail FROM diagnostics WHERE code=? LIMIT 1`, key).Scan(&seq, &detail)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM diagnostics WHERE code LIKE 'coverage_gap:%'`).Scan(&count); err != nil {
			return entry, err
		}
		if count >= coverageDiagnosticLimit-1 {
			entry = compactCoverageEntry(entry)
			key = coverageKey(entry)
			err = tx.QueryRowContext(ctx, `SELECT seq, detail FROM diagnostics WHERE code=? LIMIT 1`, key).Scan(&seq, &detail)
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return entry, err
	}
	if err == nil {
		var old coverageEntry
		if json.Unmarshal([]byte(detail), &old) == nil {
			entry = mergeCoverageEntry(entry, old)
		}
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return entry, err
	}
	if seq == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO diagnostics(at_ms,code,detail) VALUES(?,?,?)`, entry.StartMs, key, string(payload))
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE diagnostics SET at_ms=?,detail=? WHERE seq=?`, entry.StartMs, string(payload), seq)
	}
	if err != nil {
		return entry, err
	}
	return entry, nil
}

func coverageMatches(entry coverageEntry, scope Scope) bool {
	return (entry.ScopeID == "" || entry.ScopeID == scope.ScopeID) &&
		(entry.AccountDigest == "" || entry.AccountDigest == scope.AccountDigest) &&
		(entry.BridgeDigest == "" || entry.BridgeDigest == scope.BridgeDigest) &&
		(entry.TenantDigest == "" || entry.TenantDigest == coverageTenantDigest(scope.TenantID))
}

// QueryCoverage reads collection history independently of model filters.
// Zero range endpoints mean unbounded. Failures return an error rather than
// a misleading healthy/zero result. Gaps are clipped to the requested range.
func (s *Store) QueryCoverage(ctx context.Context, scope Scope, start, end time.Time) (CoverageMetadata, error) {
	out := CoverageMetadata{StorageStatus: "ok", CoverageStatus: "unknown", Gaps: []CoverageGap{}}
	if scope.ScopeID == "" || (!start.IsZero() && !end.IsZero() && !start.Before(end)) {
		return out, fmt.Errorf("%w: invalid coverage query", ErrInvalidRecord)
	}
	ctx, cancel := boundedQueryContext(ctx)
	defer cancel()
	if err := s.awaitMaintenance(ctx); err != nil {
		return out, err
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return out, err
	}
	defer release()
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var earliest sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT MIN(at_ms) FROM (
		SELECT collection_started_at_ms AS at_ms FROM scopes WHERE scope_id=?
		UNION ALL SELECT MIN(started_at_ms) FROM events WHERE scope_id=? AND phase='start'
	)`, scope.ScopeID, scope.ScopeID).Scan(&earliest)
	if err != nil {
		return out, err
	}
	if earliest.Valid {
		at := time.UnixMilli(earliest.Int64).UTC()
		out.CollectionStartedAt = &at
		if !start.IsZero() && !start.Before(at) {
			out.CoverageStatus = "known"
		}
	}
	lo, hi := int64(-1<<63), int64(1<<63-1)
	if !start.IsZero() {
		lo = start.UTC().UnixMilli()
	}
	if !end.IsZero() {
		hi = end.UTC().UnixMilli()
	}
	add := func(entry coverageEntry, memory bool) {
		if !coverageMatches(entry, scope) || entry.EndMs <= lo || entry.StartMs >= hi {
			return
		}
		if !validCoverageReason(entry.Reason) {
			entry.Reason = "collection_gap"
		}
		if entry.StartMs < lo {
			entry.StartMs = lo
		}
		if entry.EndMs > hi {
			entry.EndMs = hi
		}
		out.Gaps = append(out.Gaps, CoverageGap{StartAt: time.UnixMilli(entry.StartMs).UTC(), EndAt: time.UnixMilli(entry.EndMs).UTC(), Reason: entry.Reason})
		if memory || entry.Reason == "begin_failed" || entry.Reason == "finish_failed" {
			out.StorageStatus = "degraded"
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT start_at_ms,end_at_ms,reason FROM coverage_gaps WHERE scope_id=? AND start_at_ms<? AND end_at_ms>? ORDER BY start_at_ms LIMIT ?`, scope.ScopeID, hi, lo, coverageDiagnosticLimit+1)
	if err != nil {
		return out, err
	}
	count := 0
	for rows.Next() {
		var entry coverageEntry
		if err := rows.Scan(&entry.StartMs, &entry.EndMs, &entry.Reason); err != nil {
			rows.Close()
			return out, err
		}
		add(entry, false)
		count++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	if count > coverageDiagnosticLimit {
		var entry coverageEntry
		entry.Reason = "coverage_history_compacted"
		if err := tx.QueryRowContext(ctx, `SELECT MIN(start_at_ms),MAX(end_at_ms) FROM coverage_gaps WHERE scope_id=? AND start_at_ms<? AND end_at_ms>?`, scope.ScopeID, hi, lo).Scan(&entry.StartMs, &entry.EndMs); err != nil {
			return out, err
		}
		out.Gaps = out.Gaps[:0]
		add(entry, false)
	}
	rows, err = tx.QueryContext(ctx, `SELECT at_ms,detail FROM diagnostics WHERE code LIKE 'coverage_gap:%' AND at_ms<? ORDER BY at_ms LIMIT ?`, hi, coverageDiagnosticLimit)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var at int64
		var payload string
		if err := rows.Scan(&at, &payload); err != nil {
			rows.Close()
			return out, err
		}
		var entry coverageEntry
		if json.Unmarshal([]byte(payload), &entry) != nil || entry.EndMs <= entry.StartMs || !validCoverageDigest(entry.ScopeID) || !validCoverageDigest(entry.AccountDigest) || !validCoverageDigest(entry.BridgeDigest) || !validCoverageDigest(entry.TenantDigest) {
			entry = coverageEntry{Reason: "collection_gap", StartMs: at, EndMs: at + 1}
		}
		add(entry, false)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	var disabledSince int64
	err = tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, captureDisabledSinceKey).Scan(&disabledSince)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		until := s.now().UTC().UnixMilli() + 1
		if until <= disabledSince {
			until = disabledSince + 1
		}
		add(coverageEntry{Reason: "capture_disabled", StartMs: disabledSince, EndMs: until}, false)
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	for _, entry := range s.coverage.snapshot() {
		add(entry, true)
	}
	sort.Slice(out.Gaps, func(i, j int) bool {
		if out.Gaps[i].StartAt.Equal(out.Gaps[j].StartAt) {
			return out.Gaps[i].Reason < out.Gaps[j].Reason
		}
		return out.Gaps[i].StartAt.Before(out.Gaps[j].StartAt)
	})
	unique := out.Gaps[:0]
	for _, gap := range out.Gaps {
		if len(unique) > 0 && unique[len(unique)-1] == gap {
			continue
		}
		unique = append(unique, gap)
	}
	out.Gaps = unique
	if len(out.Gaps) > 0 {
		out.CoverageStatus = "gap"
	}
	return out, nil
}

type coverageRecorder struct{ Recorder }

// WithCoverageDiagnostics preserves the Recorder contract and reports failed
// writes through the optional coverage sink, without altering business errors.
func WithCoverageDiagnostics(rec Recorder) Recorder {
	if rec == nil {
		return nil
	}
	if _, ok := rec.(*coverageRecorder); ok {
		return rec
	}
	return &coverageRecorder{Recorder: rec}
}

func (rec *coverageRecorder) RecordCoverageGap(ctx context.Context, diagnostic CoverageDiagnostic) error {
	if sink, ok := rec.Recorder.(interface {
		RecordCoverageGap(context.Context, CoverageDiagnostic) error
	}); ok {
		return sink.RecordCoverageGap(ctx, diagnostic)
	}
	return nil
}

func (rec *coverageRecorder) recordFailure(ctx context.Context, scope Scope, at time.Time, reason string) {
	// Recorders are extensible; validate optional identifiers before forwarding.
	// Never copy arbitrary recorder error messages into the diagnostic.
	d := CoverageDiagnostic{At: at, Reason: reason, TenantID: scope.TenantID}
	if validCoverageDigest(scope.ScopeID) {
		d.ScopeID = scope.ScopeID
	}
	if validCoverageDigest(scope.AccountDigest) {
		d.AccountDigest = scope.AccountDigest
	}
	if validCoverageDigest(scope.BridgeDigest) {
		d.BridgeDigest = scope.BridgeDigest
	}
	_ = rec.RecordCoverageGap(ctx, d)
}

func (rec *coverageRecorder) Begin(ctx context.Context, start StartRecord) error {
	ctx, cancel := boundedCoverageWriteContext(ctx, writeBudget)
	defer cancel()
	err := rec.Recorder.Begin(ctx, start)
	if err != nil {
		rec.recordFailure(ctx, start.Scope, start.StartedAt, "begin_failed")
	}
	return err
}

func (rec *coverageRecorder) Finish(ctx context.Context, finish FinishRecord) error {
	ctx, cancel := boundedCoverageWriteContext(ctx, writeBudget)
	defer cancel()
	err := rec.Recorder.Finish(ctx, finish)
	if err != nil {
		rec.recordFailure(ctx, finish.FrozenStart.Scope, finish.FrozenStart.StartedAt, "finish_failed")
	}
	return err
}
