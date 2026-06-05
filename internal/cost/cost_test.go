package cost

import (
	"math"
	"testing"
)

func TestEstimate(t *testing.T) {
	t.Parallel()
	p := Price{InputPerMTok: 15, OutputPerMTok: 75}
	// 1M input + 1M output = 15 + 75.
	if got := p.Estimate(1_000_000, 1_000_000); math.Abs(got-90) > 1e-9 {
		t.Fatalf("Estimate = %v, want 90", got)
	}
	if got := p.Estimate(0, 0); got != 0 {
		t.Fatalf("Estimate(0,0) = %v, want 0", got)
	}
}

func TestLookupKnownModels(t *testing.T) {
	t.Parallel()
	cases := map[string]Price{
		"claude-opus-4-8":          {InputPerMTok: 15, OutputPerMTok: 75},
		"claude-opus-4-8-20251231": {InputPerMTok: 15, OutputPerMTok: 75}, // dated id still matches
		"claude-sonnet-4-6":        {InputPerMTok: 3, OutputPerMTok: 15},
		"CLAUDE-HAIKU-4-5":         {InputPerMTok: 1, OutputPerMTok: 5}, // case-insensitive
	}
	for model, want := range cases {
		got, ok := Lookup(model)
		if !ok || got != want {
			t.Errorf("Lookup(%q) = %#v,%v, want %#v", model, got, ok, want)
		}
	}
}

func TestLookupLongestPrefixWins(t *testing.T) {
	t.Parallel()
	// gpt-4o-mini must not fall through to the gpt-4o price.
	mini, ok := Lookup("gpt-4o-mini-2025")
	if !ok || mini.InputPerMTok != 0.15 {
		t.Fatalf("gpt-4o-mini = %#v,%v, want the mini price", mini, ok)
	}
	base, ok := Lookup("gpt-4o-2024")
	if !ok || base.InputPerMTok != 2.5 {
		t.Fatalf("gpt-4o = %#v,%v, want the base price", base, ok)
	}
}

func TestLookupUnknown(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"", "  ", "qwen2.5-coder", "some-local-model"} {
		if _, ok := Lookup(model); ok {
			t.Errorf("Lookup(%q) should be unknown/unpriced", model)
		}
	}
}
