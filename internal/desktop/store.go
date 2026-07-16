package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// desktopConfigPath is where the last-used start form values are persisted, so a
// restart prefills the form instead of resetting it.
func desktopConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runcode", "desktop.json"), nil
}

// defaultRequest is the start form's seed when nothing has been saved yet.
func defaultRequest() StartSessionRequest {
	return StartSessionRequest{
		Provider:       "openai",
		BaseURL:        "https://tenantapi-ai.ouchn.edu.cn/v1",
		PermissionMode: "interactive",
		// Arm automatic compaction by default so long sessions don't overflow the
		// context window; 128k suits most modern models. The user can change it (or
		// pick 关闭) in the start form.
		MaxContextTokens: 128_000,
	}
}

// LoadConfig returns the last-used session request to prefill the start form, or
// sensible defaults when none has been saved.
func (a *App) LoadConfig() StartSessionRequest {
	path, err := desktopConfigPath()
	if err != nil {
		return defaultRequest()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultRequest()
	}
	var req StartSessionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return defaultRequest()
	}
	return unprotectRequestSecrets(req)
}

// persistThinkingEffort updates only the thinking-effort field of the saved start
// request, so an in-conversation change to the reasoning strength survives a
// restart without disturbing the other persisted form values. Failures are
// non-fatal.
func (a *App) persistThinkingEffort(effort string) {
	req := a.LoadConfig()
	req.ThinkingEffort = effort
	saveConfig(req)
}

// maxRecentWorkspaces caps the MRU workspace list so the picker stays short and
// the config file doesn't grow unbounded.
const maxRecentWorkspaces = 8

// saveConfig persists the request so the next launch prefills the form. Credentials
// are encrypted at rest (DPAPI on Windows) and never written in the clear; the file
// is still 0600. Failures are non-fatal.
func saveConfig(req StartSessionRequest) {
	path, err := desktopConfigPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	// The MRU workspace list is server-owned: recompute it from the previously
	// persisted list plus the workspace being saved, ignoring whatever the frontend
	// sent (it only ever echoes back what it was given).
	prev := loadRawConfig()
	req.RecentWorkspaces = mergeRecentWorkspaces(prev.RecentWorkspaces, req.CWD)
	req.CustomModels = prev.CustomModels
	req.WebProxy = prev.WebProxy
	req = protectRequestSecrets(req)
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return
	}
	// Atomic replace (temp + rename): a crash mid-write must never leave a torn
	// config holding half of the encrypted credentials or the MRU list.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".desktop.json.*")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmpName, path)
}

// protectRequestSecrets replaces the plaintext credential fields with their
// encrypted form for persistence, so the on-disk config never holds a key in the
// clear. Where the platform has no protection available, the credential is dropped
// (the user re-enters it or supplies it via the environment) rather than stored.
func protectRequestSecrets(req StartSessionRequest) StartSessionRequest {
	req.APIKeyProtected, _ = protectSecret(req.APIKey)
	req.AuthTokenProtected, _ = protectSecret(req.AuthToken)
	req.APIKey = ""
	req.AuthToken = ""
	return req
}

// unprotectRequestSecrets restores the plaintext credentials from their encrypted
// form (for the start form), clearing the protected fields.
func unprotectRequestSecrets(req StartSessionRequest) StartSessionRequest {
	if req.APIKeyProtected != "" {
		if s, ok := unprotectSecret(req.APIKeyProtected); ok {
			req.APIKey = s
		}
	}
	if req.AuthTokenProtected != "" {
		if s, ok := unprotectSecret(req.AuthTokenProtected); ok {
			req.AuthToken = s
		}
	}
	req.APIKeyProtected = ""
	req.AuthTokenProtected = ""
	return req
}

// loadRawConfig reads the persisted request without falling back to defaults, so
// saveConfig can carry forward server-owned fields (the MRU workspace list). A
// missing/corrupt file yields a zero request, which is the correct seed.
func loadRawConfig() StartSessionRequest {
	path, err := desktopConfigPath()
	if err != nil {
		return StartSessionRequest{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return StartSessionRequest{}
	}
	var req StartSessionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return StartSessionRequest{}
	}
	return req
}

// saveRawConfig writes the raw persisted request back as-is (used by the
// custom-model CRUD, which manages its own field encryption). Failures are
// non-fatal, mirroring saveConfig.
func saveRawConfig(req StartSessionRequest) {
	path, err := desktopConfigPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// mergeRecentWorkspaces promotes cwd to the front of the MRU list, de-duplicating
// prior entries and capping the length. An empty cwd leaves the list unchanged.
func mergeRecentWorkspaces(prev []string, cwd string) []string {
	merged := make([]string, 0, len(prev)+1)
	if cwd != "" {
		merged = append(merged, cwd)
	}
	for _, ws := range prev {
		if ws == "" || ws == cwd {
			continue
		}
		merged = append(merged, ws)
		if len(merged) >= maxRecentWorkspaces {
			break
		}
	}
	return merged
}
