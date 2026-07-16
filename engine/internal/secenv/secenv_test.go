package secenv

import (
	"strings"
	"testing"
)

func TestSanitizeStripsSecretsKeepsRest(t *testing.T) {
	t.Parallel()
	in := []string{
		"PATH=/usr/bin",
		"HOME=/home/u",
		"LANG=en_US.UTF-8",
		"SSH_AUTH_SOCK=/tmp/ssh-agent.sock",
		"ANTHROPIC_API_KEY=sk-ant-secret",
		"OPENAI_API_KEY=sk-openai-secret",
		"ANTHROPIC_AUTH_TOKEN=tok-secret",
		"GITHUB_TOKEN=ghp_secret",
		"AWS_SECRET_ACCESS_KEY=abc",
		"DB_PASSWORD=hunter2",
		"MY_CREDENTIAL=zzz",
		"NO_EQUALS_ENTRY",
	}
	out := Sanitize(in)
	got := map[string]bool{}
	for _, e := range out {
		name := e
		if i := strings.IndexByte(e, '='); i > 0 {
			name = e[:i]
		}
		got[name] = true
	}

	mustKeep := []string{"PATH", "HOME", "LANG", "SSH_AUTH_SOCK", "NO_EQUALS_ENTRY"}
	for _, k := range mustKeep {
		if !got[k] {
			t.Errorf("Sanitize dropped a non-secret %q", k)
		}
	}
	mustStrip := []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_AUTH_TOKEN", "GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "DB_PASSWORD", "MY_CREDENTIAL"}
	for _, k := range mustStrip {
		if got[k] {
			t.Errorf("Sanitize leaked a secret %q into the child environment", k)
		}
	}
}

func TestSanitizeIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	out := Sanitize([]string{"my_api_key=x", "Path=/bin"})
	for _, e := range out {
		if strings.HasPrefix(strings.ToLower(e), "my_api_key=") {
			t.Fatalf("lowercase secret my_api_key was not stripped: %v", out)
		}
	}
}
