package imagegentool

import (
	"strings"
	"testing"
)

func TestClosestSupportedSizePrefersAspectThenPixels(t *testing.T) {
	supported := []string{"1024x1024", "1280x720", "720x1280"}
	if got := closestSupportedSize("1920x1080", supported); got != "1280x720" {
		t.Fatalf("landscape closest = %s", got)
	}
	if got := closestSupportedSize("1080x1920", supported); got != "720x1280" {
		t.Fatalf("portrait closest = %s", got)
	}
	if got := closestSupportedSize("900x900", supported); got != "1024x1024" {
		t.Fatalf("square closest = %s", got)
	}
	if got := closestSupportedSize("1600*900", supported); got != "1280x720" {
		t.Fatalf("star syntax landscape closest = %s", got)
	}
}

func TestFormatSupportedSizesNote(t *testing.T) {
	note := formatSupportedSizesNote([]string{"1024x1024", "1280x720"})
	for _, part := range []string{"1024×1024", "1280×720", "像素"} {
		if !strings.Contains(note, part) {
			t.Fatalf("note missing %q: %q", part, note)
		}
	}
}

func TestDefaultOutboundSizePrefersSmallestSquare(t *testing.T) {
	if got := DefaultOutboundSize([]string{"2048x2048", "1024x1024", "1280x720"}); got != "1024x1024" {
		t.Fatalf("smallest square = %q, want 1024x1024", got)
	}
	if got := DefaultOutboundSize([]string{"2048x2048", "768x768", "1024x1024"}); got != "768x768" {
		t.Fatalf("among squares = %q, want 768x768", got)
	}
	// 无正方形时退到面积最小的一项。
	if got := DefaultOutboundSize([]string{"2048x768", "768x2048", "1280x720"}); got != "1280x720" {
		t.Fatalf("no square fallback = %q, want 1280x720", got)
	}
	if got := DefaultOutboundSize(nil); got != "" {
		t.Fatalf("empty list = %q", got)
	}
}

func TestParseSizeUsesOneCanonicalRule(t *testing.T) {
	for _, input := range []string{"1024x768", "1024*768", "1024×768", " 1024 X 768 "} {
		w, h, canonical, ok := ParseSize(input)
		if !ok || w != 1024 || h != 768 || canonical != "1024x768" {
			t.Fatalf("ParseSize(%q) = %d,%d,%q,%v", input, w, h, canonical, ok)
		}
	}
	if _, _, _, ok := ParseSize("1024"); ok {
		t.Fatal("missing height accepted")
	}
}
