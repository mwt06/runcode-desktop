// Package secenv scrubs secret-looking variables out of the environment handed to
// child processes (the Bash tool's commands, MCP stdio servers), so a permitted or
// injected command cannot read the agent's own API keys / tokens and exfiltrate
// them. It is a silent, deterministic filter — it never prompts.
package secenv

import "strings"

// secretMarkers are case-insensitive substrings of an environment variable NAME
// that mark it as a credential to withhold from child processes. They are chosen
// to catch real secrets (API keys, tokens, passwords) without matching ordinary
// variables a command needs — e.g. "AUTH_TOKEN"/"TOKEN" strip auth tokens but the
// bare "AUTH" of SSH_AUTH_SOCK is left alone.
var secretMarkers = []string{
	"API_KEY",
	"APIKEY",
	"ACCESS_KEY",
	"SECRET_KEY",
	"PRIVATE_KEY",
	"SECRET",
	"TOKEN",
	"PASSWORD",
	"PASSWD",
	"PASSPHRASE",
	"CREDENTIAL",
}

// Sanitize returns a copy of env with credential-looking variables removed. Entries
// are "NAME=VALUE" strings (as from os.Environ). An entry with no '=' is kept.
func Sanitize(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		eq := strings.IndexByte(entry, '=')
		if eq <= 0 {
			out = append(out, entry)
			continue
		}
		if isSecretName(entry[:eq]) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// isSecretName reports whether an environment variable name looks like a secret.
func isSecretName(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range secretMarkers {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}
