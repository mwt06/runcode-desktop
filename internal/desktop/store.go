package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// configMu serializes every read-modify-write cycle on desktop.json. All mutators
// rewrite the whole file from a fresh read, and they are Wails-bound methods the
// frontend can invoke concurrently — without the lock, two overlapping saves would
// start from the same stale snapshot and the later write would silently drop the
// earlier one's change. Pure readers (LoadConfig, loadRawConfig) don't need it:
// writes are atomic replaces, so a read sees either the old or the new file, never
// a torn one.
var configMu sync.Mutex

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
		Provider: "openai",
		BaseURL:  "https://tenantapi-ai.ouchn.edu.cn/v1",
		// 交互模式：每个动作都问人，模型不代替用户放行任何东西。
		//
		// 记一笔它的代价，省得下次又当成 bug 去查：引擎 authorizerForMode 在这个模式
		// 下会把 HarmJudge 摘掉，所以 harm 判定链路上挂着的东西默认都不跑——包括把被
		// 执行脚本的内容读进判定的那条（harm_script.go）。要用得上，用户得自己切到
		// 智能模式。这是有意的取舍，不是漏接线。
		PermissionMode: "interactive",
		// Arm automatic compaction by default so long sessions don't overflow the
		// context window. The user can change it (or pick 关闭) in the start form.
		//
		// 260k assumes the connected model's window is larger than that — compaction
		// fires when usage approaches this budget, so a budget above the window means
		// the request is rejected for length before compaction ever runs. Models with
		// a 200k window need one of the smaller options.
		MaxContextTokens: 260_000,
	}
}

// LoadConfig returns a redacted view to prefill the start/settings forms, or
// sensible defaults when none has been saved.
func (a *App) LoadConfig() SettingsView {
	cfg, ok := loadRawConfigOK()
	if !ok {
		cfg = configFromRequest(defaultRequest())
	}
	return cfg.view()
}

// persistThinkingEffort updates only the thinking-effort field of the saved start
// request, so an in-conversation change to the reasoning strength survives a
// restart without disturbing the other persisted form values. Failures are
// non-fatal. The lock spans the read and the write: a concurrent save between the
// two would otherwise be clobbered by this stale snapshot.
func (a *App) persistThinkingEffort(effort string) {
	if err := updateRawConfig(func(cfg *desktopConfig) error {
		cfg.ThinkingEffort = effort
		return nil
	}); err != nil {
		debugLog("persist thinking effort: %v", err)
	}
}

// maxRecentWorkspaces caps the MRU workspace list so the picker stays short and
// the config file doesn't grow unbounded.
const maxRecentWorkspaces = 8

// saveConfig persists the request so the next launch prefills the form. Credentials
// are encrypted at rest (DPAPI on Windows) and never written in the clear; the file
// is still 0600 (writeFileAtomic creates via CreateTemp). Failures are non-fatal.
func saveConfig(req StartSessionRequest) {
	configMu.Lock()
	defer configMu.Unlock()
	saveConfigHeld(req)
}

// saveConfigHeld is saveConfig's body; the caller must hold configMu (the
// carry-forward read below and the write form one read-modify-write cycle).
func saveConfigHeld(input StartSessionRequest) {
	// 从现有存储出发，只写启动命令拥有的字段。以后增加应用级设置，不必再来补一行沿用。
	cfg, hadPrev := loadRawConfigOK()
	req := protectConfigSecrets(configFromRequest(input))
	cfg.CWD = req.CWD
	cfg.Provider = req.Provider
	cfg.Model = req.Model
	cfg.CustomModelName = req.CustomModelName
	cfg.BaseURL = req.BaseURL
	cfg.APIKey = req.APIKey
	cfg.AuthToken = req.AuthToken
	cfg.PermissionMode = req.PermissionMode
	cfg.ReasoningScenario = req.ReasoningScenario
	cfg.ThinkingEffort = req.ThinkingEffort
	cfg.HarmJudgeModel = req.HarmJudgeModel
	cfg.HarmJudgeVotes = req.HarmJudgeVotes
	cfg.Resume = req.Resume
	cfg.Continue = req.Continue
	cfg.APIKeyProtected = req.APIKeyProtected
	cfg.AuthTokenProtected = req.AuthTokenProtected
	cfg.RecentWorkspaces = mergeRecentWorkspaces(cfg.RecentWorkspaces, req.CWD)
	// 上下文设置由设置页拥有；首次启动才采用请求的默认种子。
	if !hadPrev {
		cfg.MaxTokens, cfg.MaxContextTokens, cfg.MaxHistoryMessages = req.MaxTokens, req.MaxContextTokens, req.MaxHistoryMessages
	}
	// 空租户表示本次会话无关，显式清空只能走 SetActiveTenant。
	if strings.TrimSpace(req.TenantID) != "" {
		cfg.TenantID = req.TenantID
	}
	if err := writeConfigHeld(cfg); err != nil {
		debugLog("persist session config: %v", err)
	}
}

// protectConfigSecrets replaces the plaintext credential fields with their
// encrypted form for persistence, so the on-disk config never holds a key in the
// clear. Where the platform has no protection available, the credential is dropped
// (the user re-enters it or supplies it via the environment) rather than stored.
func protectConfigSecrets(req desktopConfig) desktopConfig {
	req.APIKeyProtected, _ = protectSecret(req.APIKey)
	req.AuthTokenProtected, _ = protectSecret(req.AuthToken)
	req.APIKey = ""
	req.AuthToken = ""
	return req
}

// unprotectConfigSecrets restores credentials only for backend session assembly.
func unprotectConfigSecrets(req desktopConfig) desktopConfig {
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

// loadRawConfig reads the persisted config without falling back to defaults, so
// saveConfig can carry forward server-owned fields (the MRU workspace list). A
// missing/corrupt file yields a zero request, which is the correct seed.
func loadRawConfig() desktopConfig {
	req, _ := loadRawConfigOK()
	return req
}

// loadRawConfigOK is loadRawConfig plus "was there anything to read". The flag
// matters where a zero value is a real setting rather than "unset": carrying a
// missing file's zeros forward would silently clear a seeded default.
func loadRawConfigOK() (desktopConfig, bool) {
	path, err := desktopConfigPath()
	if err != nil {
		return desktopConfig{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return desktopConfig{}, false
	}
	var req desktopConfig
	if err := json.Unmarshal(data, &req); err != nil {
		return desktopConfig{}, false
	}
	return req, true
}

// withStoredContextLimits 用落盘的那份覆盖请求里的「上下文长度控制」三项。
//
// 它们由设置页独占（见 saveConfigHeld 里的沿用规则），所以磁盘上的那份就是事实:
// 起始页发来的零值不是"用户清空了"，只是"这个表单不管这些字段"。不填回来的话，
// 设置里填的最大输出 tokens 对新会话根本不生效——丢设置与不生效是同一个根因。
// 没有配置文件时（首次启动）保留请求自带的值，那是 defaultRequest 播的种子。
func withStoredContextLimits(req StartSessionRequest) StartSessionRequest {
	prev, ok := loadRawConfigOK()
	if !ok {
		return req
	}
	req.MaxTokens = prev.MaxTokens
	req.MaxContextTokens = prev.MaxContextTokens
	req.MaxHistoryMessages = prev.MaxHistoryMessages
	return req
}

// updateRawConfig applies mutate to a fresh read of the persisted config and
// writes the result back atomically, all under configMu. It is the only way to
// change individual persisted fields (custom models, web proxy, tenant): callers
// that did their own load→mutate→save would race other writers and lose updates.
// Validation that depends on the current snapshot belongs in mutate; returning an
// error aborts without writing. Persistence failures are returned so settings UIs
// never report success for a change that did not reach disk.
func updateRawConfig(mutate func(*desktopConfig) error) error {
	configMu.Lock()
	defer configMu.Unlock()
	cfg := loadRawConfig()
	if err := mutate(&cfg); err != nil {
		return err
	}
	return writeConfigHeld(cfg)
}

// writeConfigHeld is the single atomic persistence path; caller holds configMu.
func writeConfigHeld(cfg desktopConfig) error {
	path, err := desktopConfigPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Dir(path), filepath.Base(path), data)
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
