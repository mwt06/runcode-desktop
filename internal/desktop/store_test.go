package desktop

import (
	"strings"
	"testing"
)

func TestMergeRecentWorkspaces(t *testing.T) {
	t.Parallel()

	t.Run("promotes current workspace to front", func(t *testing.T) {
		got := mergeRecentWorkspaces([]string{"/a", "/b"}, "/b")
		if strings.Join(got, ",") != "/b,/a" {
			t.Fatalf("got %v, want [/b /a]", got)
		}
	})

	t.Run("prepends a new workspace", func(t *testing.T) {
		got := mergeRecentWorkspaces([]string{"/a", "/b"}, "/c")
		if strings.Join(got, ",") != "/c,/a,/b" {
			t.Fatalf("got %v, want [/c /a /b]", got)
		}
	})

	t.Run("empty cwd leaves the list unchanged", func(t *testing.T) {
		got := mergeRecentWorkspaces([]string{"/a", "/b"}, "")
		if strings.Join(got, ",") != "/a,/b" {
			t.Fatalf("got %v, want [/a /b]", got)
		}
	})

	t.Run("caps the list length", func(t *testing.T) {
		prev := make([]string, maxRecentWorkspaces+4)
		for i := range prev {
			prev[i] = string(rune('a' + i))
		}
		got := mergeRecentWorkspaces(prev, "/new")
		if len(got) != maxRecentWorkspaces {
			t.Fatalf("len = %d, want %d", len(got), maxRecentWorkspaces)
		}
		if got[0] != "/new" {
			t.Fatalf("front = %q, want /new", got[0])
		}
	})

	t.Run("drops blank prior entries", func(t *testing.T) {
		got := mergeRecentWorkspaces([]string{"", "/a", ""}, "/a")
		if strings.Join(got, ",") != "/a" {
			t.Fatalf("got %v, want [/a]", got)
		}
	})
}
