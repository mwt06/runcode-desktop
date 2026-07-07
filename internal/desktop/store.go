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
	return req
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

// saveConfig persists the request (0600 — it may carry an API key) so the next
// launch prefills the form. Failures are non-fatal.
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
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
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
