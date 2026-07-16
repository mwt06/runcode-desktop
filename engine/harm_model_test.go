package engine

import "testing"

func TestResolveHarmModel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"explicit wins", Config{HarmJudgeModel: "custom-judge", Provider: "anthropic", Model: "opus"}, "custom-judge"},
		{"anthropic default is independent", Config{Provider: "anthropic", Model: "claude-opus-4-8"}, defaultHarmJudgeModel},
		{"empty provider defaults to anthropic", Config{Model: "claude-opus-4-8"}, defaultHarmJudgeModel},
		{"other provider reuses main model", Config{Provider: "openai", Model: "gpt-5"}, "gpt-5"},
		{"explicit wins even for openai", Config{HarmJudgeModel: "j", Provider: "openai", Model: "gpt-5"}, "j"},
	}
	for _, c := range cases {
		if got := resolveHarmModel(c.cfg); got != c.want {
			t.Fatalf("%s: resolveHarmModel = %q, want %q", c.name, got, c.want)
		}
	}
}
