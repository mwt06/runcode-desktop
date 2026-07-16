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

	env := childEnv()

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
