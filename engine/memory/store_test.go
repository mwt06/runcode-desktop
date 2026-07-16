package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreLoadMissingFilesIsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStore(Options{
		UserPath:    filepath.Join(dir, "user.md"),
		ProjectPath: filepath.Join(dir, "proj.md"),
	})
	l, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !l.Empty() {
		t.Fatalf("missing files should yield empty memory: %#v", l)
	}
}

func TestStoreAppendCreatesDedupsAndLoads(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".runcode", "memory.md") // parent must be created
	s := NewStore(Options{ProjectPath: path})

	r1, err := s.Append(ScopeProject, "uses Go modules")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !r1.Added || r1.Duplicate {
		t.Fatalf("first append should add: %#v", r1)
	}

	// Case-insensitive duplicate is skipped.
	r2, err := s.Append(ScopeProject, "USES GO MODULES")
	if err != nil {
		t.Fatalf("Append dup: %v", err)
	}
	if r2.Added || !r2.Duplicate {
		t.Fatalf("equivalent append should dedup: %#v", r2)
	}

	l, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(l.Project) != 1 || l.Project[0] != "uses Go modules" {
		t.Fatalf("project entries = %#v, want one original", l.Project)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !strings.Contains(string(data), "# runcode memory") {
		t.Fatalf("file missing header:\n%s", data)
	}
}

func TestStoreAppendUserScope(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStore(Options{UserPath: filepath.Join(dir, "memory.md")})
	if _, err := s.Append(ScopeUser, "prefers tabs over spaces"); err != nil {
		t.Fatalf("Append user: %v", err)
	}
	l, _ := s.Load()
	if len(l.User) != 1 || len(l.Project) != 0 {
		t.Fatalf("expected one user entry, no project: %#v", l)
	}
}

func TestStoreAppendUnavailableScope(t *testing.T) {
	t.Parallel()
	// Only project configured; saving to user must fail cleanly.
	s := NewStore(Options{ProjectPath: filepath.Join(t.TempDir(), "memory.md")})
	if _, err := s.Append(ScopeUser, "x"); !errors.Is(err, ErrScopeUnavailable) {
		t.Fatalf("err = %v, want ErrScopeUnavailable", err)
	}
}

func TestStoreAppendRejectsEmptyAndUnknownScope(t *testing.T) {
	t.Parallel()
	s := NewStore(Options{ProjectPath: filepath.Join(t.TempDir(), "memory.md")})
	if _, err := s.Append(ScopeProject, "   "); !errors.Is(err, ErrEmptyFact) {
		t.Fatalf("blank fact err = %v, want ErrEmptyFact", err)
	}
	if _, err := s.Append(Scope("weird"), "fact"); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("unknown scope err = %v, want ErrInvalidScope", err)
	}
}

func TestStoreLoadTruncates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.md")
	body := strings.Repeat("- "+strings.Repeat("a", 80)+"\n", 50)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(Options{ProjectPath: path, MaxBytes: 100})
	l, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !l.Truncated {
		t.Fatal("oversized file should report truncation")
	}
}
