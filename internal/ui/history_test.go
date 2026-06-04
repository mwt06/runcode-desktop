package ui

import "testing"

func TestPromptHistoryEmpty(t *testing.T) {
	t.Parallel()

	var h promptHistory
	if _, ok := h.older("draft"); ok {
		t.Fatal("older on empty history should report ok=false")
	}
	if _, ok := h.newer(); ok {
		t.Fatal("newer on empty history should report ok=false")
	}
}

func TestPromptHistoryWalksOlderThenNewer(t *testing.T) {
	t.Parallel()

	var h promptHistory
	h.add("first")
	h.add("second")

	// Start on the live draft "wip"; the first Up saves it and shows "second".
	if got, ok := h.older("wip"); !ok || got != "second" {
		t.Fatalf("older#1 = %q,%v, want second,true", got, ok)
	}
	if got, ok := h.older("ignored"); !ok || got != "first" {
		t.Fatalf("older#2 = %q,%v, want first,true", got, ok)
	}
	// At the oldest entry it stays put.
	if got, ok := h.older("ignored"); !ok || got != "first" {
		t.Fatalf("older#3 = %q,%v, want first,true (clamped)", got, ok)
	}
	// Walking forward returns to "second" and then to the saved draft.
	if got, ok := h.newer(); !ok || got != "second" {
		t.Fatalf("newer#1 = %q,%v, want second,true", got, ok)
	}
	if got, ok := h.newer(); !ok || got != "wip" {
		t.Fatalf("newer#2 = %q,%v, want restored draft wip,true", got, ok)
	}
	if _, ok := h.newer(); ok {
		t.Fatal("newer past the live line should report ok=false")
	}
}

func TestPromptHistoryIgnoresEmptyAndConsecutiveDuplicates(t *testing.T) {
	t.Parallel()

	var h promptHistory
	h.add("")
	h.add("cmd")
	h.add("cmd")
	h.add("other")

	want := []string{"other", "cmd"}
	for i, w := range want {
		got, ok := h.older("")
		if !ok || got != w {
			t.Fatalf("older#%d = %q,%v, want %q", i, got, ok, w)
		}
	}
	if _, ok := h.older(""); ok && h.pos != 0 {
		t.Fatalf("history should hold exactly two entries, pos=%d", h.pos)
	}
}

func TestPromptHistoryAddResetsNavigation(t *testing.T) {
	t.Parallel()

	var h promptHistory
	h.add("a")
	h.add("b")
	if _, ok := h.older("draft"); !ok { // navigate away from the live line
		t.Fatal("expected older to succeed")
	}
	h.add("c") // a new submission must snap navigation back to the live line
	if _, ok := h.newer(); ok {
		t.Fatal("after add, newer should report ok=false (already live)")
	}
	if got, ok := h.older(""); !ok || got != "c" {
		t.Fatalf("older after add = %q,%v, want c,true", got, ok)
	}
}
