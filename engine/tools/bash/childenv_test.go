package bash

import (
	"strings"
	"testing"
)

// Spawned commands must not inherit the agent's credentials, so a permitted or
// injected command can't read a key and exfiltrate it.
func TestChildEnvScrubsSecretsKeepsUTF8(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
	t.Setenv("GITHUB_TOKEN", "ghp_secret")

	env := childEnv(nil)

	for _, e := range env {
		if strings.HasPrefix(e, "ANTHROPIC_API_KEY=") || strings.HasPrefix(e, "GITHUB_TOKEN=") {
			t.Fatalf("childEnv leaked a secret to spawned commands: %q", e)
		}
	}
	var utf8 bool
	for _, e := range env {
		if e == "PYTHONUTF8=1" {
			utf8 = true
		}
	}
	if !utf8 {
		t.Fatal("childEnv dropped the PYTHONUTF8 setting")
	}
}

// countPrefix counts entries whose name (case-insensitive) matches name.
func countEnvName(env []string, name string) (count int, last string) {
	for _, e := range env {
		i := strings.IndexByte(e, '=')
		if i < 0 {
			continue
		}
		if strings.EqualFold(e[:i], name) {
			count++
			last = e[i+1:]
		}
	}
	return count, last
}

// Host-injected extras must replace an inherited variable of the same name —
// exactly one entry survives and it carries the injected value.
func TestChildEnvExtraOverridesInherited(t *testing.T) {
	t.Setenv("RUNCODE_CHILDENV_OVERRIDE", "inherited")

	env := childEnv(map[string]string{"RUNCODE_CHILDENV_OVERRIDE": "injected"})

	count, val := countEnvName(env, "RUNCODE_CHILDENV_OVERRIDE")
	if count != 1 || val != "injected" {
		t.Fatalf("got %d entries, last value %q; want exactly 1 entry = injected", count, val)
	}
}

// Windows environment names are case-insensitive, so a differently-cased
// override must still displace the inherited entry rather than duplicate it.
func TestChildEnvOverrideIsCaseInsensitive(t *testing.T) {
	t.Setenv("RUNCODE_CHILDENV_CASE", "inherited")

	env := childEnv(map[string]string{"runcode_childenv_case": "injected"})

	count, val := countEnvName(env, "RUNCODE_CHILDENV_CASE")
	if count != 1 || val != "injected" {
		t.Fatalf("got %d case-insensitive entries, last value %q; want exactly 1 entry = injected", count, val)
	}
}

// The fixed appends (PYTHONUTF8 etc.) are part of the pre-merge base, so an
// explicit extra wins over them too.
func TestChildEnvExtraOverridesFixedAppends(t *testing.T) {
	env := childEnv(map[string]string{"PYTHONUTF8": "0"})

	count, val := countEnvName(env, "PYTHONUTF8")
	if count != 1 || val != "0" {
		t.Fatalf("got %d PYTHONUTF8 entries, last value %q; want exactly 1 entry = 0", count, val)
	}
}

// Sanitize keeps scrubbing the *inherited* environment even when extras are
// present, while the extras themselves — explicit, trusted host injection — are
// not scrubbed even if their names look like secrets.
func TestChildEnvSanitizesInheritedButNotExtra(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_secret") // inherited secret → must be scrubbed

	env := childEnv(map[string]string{"RUNCODE_SESSION_TOKEN": "explicit"})

	if count, _ := countEnvName(env, "GITHUB_TOKEN"); count != 0 {
		t.Fatal("inherited secret GITHUB_TOKEN leaked despite extras being present")
	}
	count, val := countEnvName(env, "RUNCODE_SESSION_TOKEN")
	if count != 1 || val != "explicit" {
		t.Fatalf("explicit extra was scrubbed: %d entries, value %q", count, val)
	}
}

// nil and empty extras are the historical inherit-only environment.
func TestChildEnvEmptyExtraMatchesNil(t *testing.T) {
	a := childEnv(nil)
	b := childEnv(map[string]string{})
	if len(a) != len(b) {
		t.Fatalf("nil vs empty extra diverged: %d vs %d entries", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("entry %d diverged: %q vs %q", i, a[i], b[i])
		}
	}
}
