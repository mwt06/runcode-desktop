package vision

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gitlab.ouc-online.com.cn/aibase/agentloop/imageinput"
)

const maxStoreBytes = 8 << 20

// Store is a rooted, atomic per-session sidecar containing no image bytes.
type Store struct {
	workspace, name string
	mu              sync.Mutex
}

// NewStore binds an analysis store to its owner, never to a UI focus pointer.
func NewStore(workspace, session string) *Store {
	hash := sha256.Sum256([]byte(session))
	return &Store{workspace: workspace, name: filepath.Join(".runcode", "image-analysis", hex.EncodeToString(hash[:])+".json")}
}

// List loads bounded records; callers may treat corruption as a cache miss.
func (s *Store) List(ctx context.Context) ([]imageinput.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return s.load(root)
}

func (s *Store) load(root *os.Root) ([]imageinput.Record, error) {
	f, err := root.Open(s.name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxStoreBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxStoreBytes {
		return nil, errors.New("image analysis cache too large")
	}
	var records []imageinput.Record
	if err := json.Unmarshal(b, &records); err != nil {
		return nil, err
	}
	if len(records) > 128 {
		records = records[len(records)-128:]
	}
	valid := records[:0]
	for _, r := range records {
		if len(r.Key) != 64 || r.RouteKey == "" || len(r.Refs) == 0 || len(r.Refs) > 8 || strings.TrimSpace(r.Answer.Text) == "" {
			continue
		}
		valid = append(valid, r)
	}
	return valid, nil
}

// Put atomically appends a result, retaining a bounded recent history.
func (s *Store) Put(ctx context.Context, record imageinput.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.workspace)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	records, err := s.load(root)
	if err != nil {
		records = nil
	}
	next := make([]imageinput.Record, 0, len(records)+1)
	for _, r := range records {
		if r.Key != record.Key {
			next = append(next, r)
		}
	}
	next = append(next, record)
	if len(next) > 128 {
		next = next[len(next)-128:]
	}
	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	for len(b) > maxStoreBytes && len(next) > 1 {
		next = next[1:]
		b, err = json.Marshal(next)
		if err != nil {
			return err
		}
	}
	if len(b) > maxStoreBytes {
		return errors.New("图片识别记录过大，无法保存")
	}
	if err := root.MkdirAll(filepath.Dir(s.name), 0700); err != nil {
		return err
	}
	tmp := s.name + "." + rand.Text() + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(tmp) }()
	_, writeErr := f.Write(b)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr, ctx.Err()); err != nil {
		return fmt.Errorf("保存图片识别结果失败：%w", err)
	}
	return root.Rename(tmp, s.name)
}

// Delete removes only this session's derived analysis data.
func (s *Store) Delete(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.workspace)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	err = root.Remove(s.name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
