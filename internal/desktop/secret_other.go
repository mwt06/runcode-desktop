//go:build !windows

package desktop

// Off Windows there is no built-in per-user secret protection wired up yet, so
// protectSecret reports ok=false and the caller drops the credential from
// desktop.json rather than persisting it in the clear (the user re-enters it or
// supplies it via the environment). A macOS Keychain / Linux Secret Service path
// is future work.
func protectSecret(string) (string, bool) { return "", false }

func unprotectSecret(string) (string, bool) { return "", false }
