package desktop

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/wt68/runcode/engine/diff"
	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/engine/transcript"
	"github.com/wt68/runcode/engine/turn"
)

// maxEditSnapshotBytes bounds a single pre/post snapshot. Larger files are skipped
// (no edit card) so undo/review never hold huge blobs in memory or on disk.
const maxEditSnapshotBytes = 4 << 20

// reviewDiffOptions bounds the review diff. It is far more generous than the tool
// step's DefaultOptions so a real edit (hundreds of lines) renders in full; past
// MaxInput it falls back to the "large file diff omitted" info line.
var reviewDiffOptions = diff.Options{Context: 3, MaxLines: 4000, MaxInput: 20000}

// EditRecord is the per-edit metadata attached to a Write/Edit tool event's Data
// (live) and returned by ListEdits (resume). Keyed to the frontend by ToolUseID.
type EditRecord struct {
	SnapshotID string `json:"snapshotId"`
	ToolUseID  string `json:"toolUseId"`
	RelPath    string `json:"relPath"`
	Added      int    `json:"added"`
	Removed    int    `json:"removed"`
	Created    bool   `json:"created"`
	Reverted   bool   `json:"reverted,omitempty"`
}

// EditDiff is the red/green review of one edit: the turn baseline vs the turn's
// latest content for that file.
type EditDiff struct {
	RelPath string            `json:"relPath"`
	Created bool              `json:"created"`
	Lines   []tool.OutputLine `json:"lines"`
}

// baselineMeta is the per-snapshot metadata (one baseline = one turn's first edit
// of a file), recovered from the index on reopen.
type baselineMeta struct {
	relPath  string
	created  bool
	reverted bool
}

// editStore captures Write/Edit pre/post content into <ws>/.runcode/edits/<sess>/
// and serves undo/review. One instance per App, rebound per session via
// BeginSession. All state is guarded by mu.
type editStore struct {
	mu       sync.Mutex
	ws       string            // workspace root; "" until BeginSession
	dir      string            // <ws>/.runcode/edits/<sessionID>; "" until BeginSession
	nextID   int               // next baseline id
	baseline map[string]string // relPath -> snapshotID, current turn only
	meta     map[string]baselineMeta
	records  []EditRecord // append-only, one per edit (per tool-use)
}

func newEditStore() *editStore {
	return &editStore{baseline: map[string]string{}, meta: map[string]baselineMeta{}}
}

// BeginSession binds the store to a session's edit directory and loads any existing
// index so undo/review survive reopen. It resets the per-turn baseline map.
func (s *editStore) BeginSession(ws, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.baseline = map[string]string{}
	s.meta = map[string]baselineMeta{}
	s.records = nil
	s.nextID = 1
	if ws == "" || sessionID == "" || transcript.ValidateSessionID(sessionID) != nil {
		s.ws = ""
		s.dir = ""
		return
	}
	s.ws = ws
	s.dir = filepath.Join(ws, ".runcode", "edits", sessionID)
	s.loadIndexLocked()
}

// BeginTurn clears the per-turn baseline map so the next edit to a file captures a
// fresh baseline (this turn's undo/review is relative to the turn's start).
func (s *editStore) BeginTurn() {
	s.mu.Lock()
	s.baseline = map[string]string{}
	s.mu.Unlock()
}

// BeginEdit implements turn.EditRecorder. It reads the pre-edit content now (before
// the tool overwrites) and returns a handle to finish on success. Returns nil to
// skip (no dir, oversized, or unreadable).
func (s *editStore) BeginEdit(relPath, toolUseID string) turn.EditHandle {
	s.mu.Lock()
	dir, ws := s.dir, s.workspaceLocked()
	s.mu.Unlock()
	if dir == "" || ws == "" {
		return nil
	}
	abs, err := resolveForWrite(ws, relPath)
	if err != nil {
		return nil
	}
	old, existed, ok := readCapped(abs)
	if !ok {
		return nil // oversized or unreadable → skip
	}
	return &editHandle{store: s, rel: filepath.ToSlash(relPath), abs: abs, toolUseID: toolUseID, old: old, existed: existed}
}

// workspaceLocked returns the bound workspace root. Caller holds mu.
func (s *editStore) workspaceLocked() string { return s.ws }

type editHandle struct {
	store     *editStore
	rel       string
	abs       string
	toolUseID string
	old       []byte
	existed   bool
}

// Commit reads the post-edit content, writes/updates the baseline + after snapshots,
// computes the cumulative stat, appends an index record, and returns the EditRecord.
func (h *editHandle) Commit() (any, error) {
	neu, _, ok := readCapped(h.abs)
	if !ok {
		return nil, nil // post-edit unreadable/oversized → attach nothing
	}
	s := h.store
	s.mu.Lock()
	defer s.mu.Unlock()

	id, isNew := s.baseline[h.rel], false
	if id == "" {
		id = strconv.Itoa(s.nextID)
		s.nextID++
		s.baseline[h.rel] = id
		isNew = true
	}
	if isNew {
		if err := s.writeSnapshotLocked("base-"+id, h.old); err != nil {
			return nil, err
		}
		s.meta[id] = baselineMeta{relPath: h.rel, created: !h.existed}
	}
	if err := s.writeSnapshotLocked("after-"+id, neu); err != nil {
		return nil, err
	}
	baseBytes, _ := s.readSnapshotLocked("base-" + id)
	added, removed := diff.Stat(string(baseBytes), string(neu))
	rec := EditRecord{
		SnapshotID: id,
		ToolUseID:  h.toolUseID,
		RelPath:    h.rel,
		Added:      added,
		Removed:    removed,
		Created:    s.meta[id].created,
	}
	s.records = append(s.records, rec)
	if err := s.appendIndexLocked(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// Revert restores the file for snapshotID to its turn baseline: a created file is
// deleted, otherwise the baseline bytes are written back. Idempotent-ish: a missing
// snapshot returns an error; a re-revert re-writes the baseline (harmless).
func (s *editStore) Revert(snapshotID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.meta[snapshotID]
	if !ok {
		return errors.New("unknown edit")
	}
	ws := s.workspaceLocked()
	abs, err := resolveForWrite(ws, m.relPath)
	if err != nil {
		return err
	}
	if m.created {
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else {
		base, err := s.readSnapshotLocked("base-" + snapshotID)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, base, 0o600); err != nil {
			return err
		}
	}
	return s.markRevertedLocked(snapshotID)
}

// Diff returns the review of snapshotID: baseline vs the turn's latest content.
func (s *editStore) Diff(snapshotID string) (EditDiff, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.meta[snapshotID]
	if !ok {
		return EditDiff{}, errors.New("unknown edit")
	}
	base, err := s.readSnapshotLocked("base-" + snapshotID)
	if err != nil {
		return EditDiff{}, err
	}
	after, err := s.readSnapshotLocked("after-" + snapshotID)
	if err != nil {
		return EditDiff{}, err
	}
	return EditDiff{RelPath: m.relPath, Created: m.created, Lines: diff.Unified(string(base), string(after), reviewDiffOptions)}, nil
}

// List returns every recorded edit (with the current reverted flag), for resume.
func (s *editStore) List() []EditRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]EditRecord, len(s.records))
	for i, r := range s.records {
		r.Reverted = s.meta[r.SnapshotID].reverted
		out[i] = r
	}
	return out
}

// --- locked helpers (caller holds mu) ---

func (s *editStore) writeSnapshotLocked(name string, data []byte) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, name), data, 0o600)
}

func (s *editStore) readSnapshotLocked(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.dir, name))
}

// indexRow is the on-disk shape of one edit; mirrors EditRecord plus nothing else.
type indexRow struct {
	SnapshotID string `json:"snapshotId"`
	ToolUseID  string `json:"toolUseId"`
	RelPath    string `json:"relPath"`
	Added      int    `json:"added"`
	Removed    int    `json:"removed"`
	Created    bool   `json:"created"`
	Reverted   bool   `json:"reverted"`
}

func (s *editStore) appendIndexLocked(rec EditRecord) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "index.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	row := indexRow{rec.SnapshotID, rec.ToolUseID, rec.RelPath, rec.Added, rec.Removed, rec.Created, false}
	b, _ := json.Marshal(row)
	_, err = f.Write(append(b, '\n'))
	return err
}

func (s *editStore) loadIndexLocked() {
	f, err := os.Open(filepath.Join(s.dir, "index.jsonl"))
	if err != nil {
		return // no prior edits
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	max := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var row indexRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		s.records = append(s.records, EditRecord{
			SnapshotID: row.SnapshotID, ToolUseID: row.ToolUseID, RelPath: row.RelPath,
			Added: row.Added, Removed: row.Removed, Created: row.Created, Reverted: row.Reverted,
		})
		m := s.meta[row.SnapshotID]
		m.relPath = row.RelPath
		m.created = row.Created
		if row.Reverted {
			m.reverted = true
		}
		s.meta[row.SnapshotID] = m
		if n, err := strconv.Atoi(row.SnapshotID); err == nil && n >= max {
			max = n
		}
	}
	s.nextID = max + 1
}

// markRevertedLocked flips the reverted flag for snapshotID in memory and rewrites
// the index so it survives reopen. The rewrite is atomic (temp file + rename) so a
// mid-write failure never truncates or corrupts the existing index.
func (s *editStore) markRevertedLocked(snapshotID string) error {
	m := s.meta[snapshotID]
	m.reverted = true
	s.meta[snapshotID] = m
	for i := range s.records {
		if s.records[i].SnapshotID == snapshotID {
			s.records[i].Reverted = true
		}
	}
	// Rewrite index.jsonl from records (small).
	var b strings.Builder
	for _, r := range s.records {
		row := indexRow{r.SnapshotID, r.ToolUseID, r.RelPath, r.Added, r.Removed, r.Created, s.meta[r.SnapshotID].reverted}
		j, _ := json.Marshal(row)
		b.Write(j)
		b.WriteByte('\n')
	}
	return writeFileAtomic(s.dir, "index.jsonl", []byte(b.String()))
}

// writeFileAtomic writes data to <dir>/<name> via a temp file + rename, so a
// mid-write failure (disk full, AV/indexer lock on Windows) never truncates or
// corrupts the existing file. os.Rename replaces the destination on both POSIX and
// Windows for a same-directory move.
func writeFileAtomic(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, name+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// readCapped reads path if it exists and is within the size cap. Returns
// (content, existed, ok). ok=false means skip (oversized or read error).
func readCapped(path string) (content []byte, existed bool, ok bool) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, false, true
	}
	if err != nil || info.IsDir() || info.Size() > maxEditSnapshotBytes {
		return nil, false, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, false
	}
	return data, true, true
}

// RevertEdit restores the file for snapshotID to its turn baseline (Wails binding).
func (a *App) RevertEdit(snapshotID string) error { return a.edits.Revert(snapshotID) }

// ReviewEdit returns the baseline-vs-latest red/green diff for snapshotID (Wails binding).
func (a *App) ReviewEdit(snapshotID string) (EditDiff, error) { return a.edits.Diff(snapshotID) }

// ListEdits returns every recorded edit for the active session, so a resumed
// session can re-render its "已编辑" cards (Wails binding).
func (a *App) ListEdits() []EditRecord { return a.edits.List() }

// resolveForWrite resolves a workspace-relative path to an absolute path for
// writing/deleting, tolerating a not-yet-existing target: it rejects lexical
// escapes and verifies the nearest existing ancestor resolves within ws (symlink
// safe), mirroring toolpath.ResolveMutationTarget. Fail-closed.
func resolveForWrite(ws, rel string) (string, error) {
	if ws == "" {
		return "", errors.New("no workspace")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || filepath.IsAbs(clean) {
		return "", errors.New("path escapes workspace")
	}
	abs := filepath.Join(ws, clean)
	anc := abs
	for {
		if _, err := os.Lstat(anc); err == nil {
			break
		}
		parent := filepath.Dir(anc)
		if parent == anc {
			break
		}
		anc = parent
	}
	realAnc, err := filepath.EvalSymlinks(anc)
	if err != nil {
		return "", err
	}
	realWs, err := filepath.EvalSymlinks(ws)
	if err != nil {
		return "", err
	}
	if realAnc != realWs && !strings.HasPrefix(realAnc, realWs+string(os.PathSeparator)) {
		return "", errors.New("path escapes workspace")
	}
	return abs, nil
}
