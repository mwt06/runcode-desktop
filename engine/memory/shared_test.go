package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSharedSamePathsSameStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	opts := Options{
		UserPath:    filepath.Join(dir, "user.md"),
		ProjectPath: filepath.Join(dir, "proj.md"),
	}
	if a, b := Shared(opts), Shared(opts); a != b {
		t.Fatal("two Shared calls with identical paths must return the same *Store")
	}
}

func TestSharedNormalizesPathSpelling(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plain := Options{ProjectPath: filepath.Join(dir, "memory.md")}
	// A redundant "." segment spells the same file differently; Clean folds it.
	dotted := Options{ProjectPath: dir + string(filepath.Separator) + "." + string(filepath.Separator) + "memory.md"}
	if a, b := Shared(plain), Shared(dotted); a != b {
		t.Fatal("path spelling variants of one file must share a Store")
	}
}

func TestSharedDifferentPathsDifferentStores(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := Shared(Options{ProjectPath: filepath.Join(dir, "a", "memory.md")})
	b := Shared(Options{ProjectPath: filepath.Join(dir, "b", "memory.md")})
	if a == b {
		t.Fatal("different paths must get distinct Stores")
	}
}

func TestSharedConcurrentSameKey(t *testing.T) {
	t.Parallel()
	opts := Options{ProjectPath: filepath.Join(t.TempDir(), "memory.md")}

	const n = 32
	stores := make([]*Store, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stores[i] = Shared(opts)
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if stores[i] != stores[0] {
			t.Fatal("concurrent Shared calls for one key returned different Stores")
		}
	}
}

// Two sessions' handles over the same file must dedup against each other: the
// registry gives them one Store, so concurrent appends of one fact serialize on
// a single lock and exactly one line lands in the file.
func TestSharedHandlesShareDedup(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".runcode", "memory.md")
	opts := Options{ProjectPath: path}
	h1, h2 := Shared(opts), Shared(opts)

	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		h := h1
		if i%2 == 1 {
			h = h2
		}
		wg.Add(1)
		go func(h *Store, i int) {
			defer wg.Done()
			if _, err := h.Append(ScopeProject, "shared fact"); err != nil {
				t.Errorf("append shared fact: %v", err)
			}
			if _, err := h.Append(ScopeProject, fmt.Sprintf("unique fact %d", i)); err != nil {
				t.Errorf("append unique fact: %v", err)
			}
		}(h, i)
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read memory file: %v", err)
	}
	if got := strings.Count(string(data), "- shared fact\n"); got != 1 {
		t.Fatalf("shared fact written %d times, want exactly 1:\n%s", got, data)
	}
	for i := 0; i < workers; i++ {
		if !strings.Contains(string(data), fmt.Sprintf("- unique fact %d\n", i)) {
			t.Fatalf("unique fact %d missing:\n%s", i, data)
		}
	}
}
