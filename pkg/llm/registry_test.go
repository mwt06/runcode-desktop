package llm

import (
	"context"
	"errors"
	"testing"
)

type fakeProvider struct{ cfg Config }

func (fakeProvider) Name() string                 { return "fake" }
func (fakeProvider) Capabilities() Capabilities   { return Capabilities{} }
func (fakeProvider) Stream(context.Context, Request) (Stream, error) {
	return nil, errors.New("not implemented")
}

func TestRegistryBuildAndLookup(t *testing.T) {
	Register("fake-build", func(cfg Config) (Provider, error) {
		return fakeProvider{cfg: cfg}, nil
	})

	if !IsRegistered("fake-build") {
		t.Fatal("registered provider not reported by IsRegistered")
	}
	p, err := Build("fake-build", Config{APIKey: "k"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := p.(fakeProvider).cfg.APIKey; got != "k" {
		t.Fatalf("config not threaded to factory: APIKey = %q", got)
	}

	found := false
	for _, name := range Registered() {
		if name == "fake-build" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Registered() missing fake-build: %v", Registered())
	}
}

func TestRegistryUnknownProviderErrors(t *testing.T) {
	if _, err := Build("does-not-exist", Config{}); err == nil {
		t.Fatal("Build of unknown provider should error, not fall back")
	}
}

func TestRegistryDuplicatePanics(t *testing.T) {
	Register("fake-dup", func(Config) (Provider, error) { return fakeProvider{}, nil })
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate registration should panic")
		}
	}()
	Register("fake-dup", func(Config) (Provider, error) { return fakeProvider{}, nil })
}

func TestParseThinkingEffort(t *testing.T) {
	cases := map[string]struct {
		want ThinkingEffort
		ok   bool
	}{
		"":       {ThinkingOff, true},
		"off":    {ThinkingOff, true},
		"low":    {ThinkingLow, true},
		"medium": {ThinkingMedium, true},
		"high":   {ThinkingHigh, true},
		"bogus":  {ThinkingOff, false},
	}
	for in, want := range cases {
		got, ok := ParseThinkingEffort(in)
		if got != want.want || ok != want.ok {
			t.Fatalf("ParseThinkingEffort(%q) = (%q,%v), want (%q,%v)", in, got, ok, want.want, want.ok)
		}
	}
	if (ThinkingConfig{Effort: ThinkingLow}).Enabled() != true || (ThinkingConfig{}).Enabled() != false {
		t.Fatal("Enabled() wrong")
	}
}
