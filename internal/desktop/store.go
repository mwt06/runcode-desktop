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
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}
