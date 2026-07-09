package diff

import "testing"

func TestStatCountsAddedAndRemoved(t *testing.T) {
	cases := []struct {
		name             string
		old, new         string
		wantAdd, wantDel int
	}{
		{"new file all additions", "", "a\nb\nc\n", 3, 0},
		{"cleared file all deletions", "a\nb\n", "", 0, 2},
		{"replace one line", "a\nb\nc\n", "a\nB\nc\n", 1, 1},
		{"append lines", "a\n", "a\nb\nc\n", 2, 0},
		{"no change", "a\nb\n", "a\nb\n", 0, 0},
		{"trailing newline irrelevant", "a\nb", "a\nb\n", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			add, del := Stat(c.old, c.new)
			if add != c.wantAdd || del != c.wantDel {
				t.Fatalf("Stat(%q,%q) = (+%d -%d), want (+%d -%d)", c.old, c.new, add, del, c.wantAdd, c.wantDel)
			}
		})
	}
}

func TestStatCountsBeyondDisplayCap(t *testing.T) {
	// 500 added lines — well past Unified's 200-line display cap. Stat must be exact.
	old := ""
	new := ""
	for i := 0; i < 500; i++ {
		new += "line\n"
	}
	add, del := Stat(old, new)
	if add != 500 || del != 0 {
		t.Fatalf("Stat = (+%d -%d), want (+500 -0)", add, del)
	}
}
