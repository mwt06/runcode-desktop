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
	// Custom models are backend-owned records and may contain OS-protected keys.
	// The renderer obtains their redacted summaries through ListCustomModels only.
	req.CustomModels = nil
	req.APIKey = ""
	req.AuthToken = ""
	req.APIKeyProtected = ""
	req.AuthTokenProtected = ""
	return req
}

// persistThinkingEffort updates only the thinking-effort field of the saved start
// request, so an in-conversation change to the reasoning strength survives a
// restart without disturbing the other persisted form values. Failures are
// non-fatal. The lock spans the read and the write: a concurrent save between the
// two would otherwise be clobbered by this stale snapshot.
func (a *App) persistThinkingEffort(effort string) {
	configMu.Lock()
	defer configMu.Unlock()
	req := a.LoadConfig()
	req.ThinkingEffort = effort
	saveConfigHeld(req)
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
func saveConfigHeld(req StartSessionRequest) {
	path, err := desktopConfigPath()
	if err != nil {
		return
	}
	// The MRU workspace list is server-owned: recompute it from the previously
	// persisted list plus the workspace being saved, ignoring whatever the frontend
	// sent (it only ever echoes back what it was given).
	prev, hadPrev := loadRawConfigOK()
	req.RecentWorkspaces = mergeRecentWorkspaces(prev.RecentWorkspaces, req.CWD)
	req.CustomModels = prev.CustomModels
	req.WebProxy = prev.WebProxy
	req.SkipLogin = prev.SkipLogin
	req.ContextAudit = prev.ContextAudit
	// 「上下文长度控制」那一组（最大输出 tokens / 上下文预算 / 历史消息上限）是**设置页
	// 独占**的：起始页不渲染它们，只按 wire 零值发过来（见 pages/start/index.tsx）。把那些
	// 零值原样落盘，等于每开一次新会话就把用户填的清一次——实测症状是「设置里改完最大
	// 输出，重启后从起始页进一次就没了」。
	//
	// 办法与 SkipLogin 同一套:这里一律沿用已落盘的值,SaveSettings 是它们唯一的写入口。
	// 只有还没有配置文件时例外——那时沿用会把 defaultRequest 播下的 260k 预算抹成 0,
	// 所以首次保存保留请求自带的种子值。
	if hadPrev {
		req.MaxTokens = prev.MaxTokens
		req.MaxContextTokens = prev.MaxContextTokens
		req.MaxHistoryMessages = prev.MaxHistoryMessages
	}
	// 租户是**账号级**选择，写它的三层职责各不相同：
	//   SetActiveTenant        权威写入，含显式设空（=用令牌自带租户）；
	//   persistConnectionChoice 完全不碰——换模型与"我属于哪个组织"无关；
	//   这里（会话级保存）      只在非空时覆盖。
	// 非空的来源都是前端当下的活动租户，与 SetActiveTenant 刚落盘的值一致，覆盖无害；
	// 而空**不表示"请清除租户"**，只表示"本次会话与租户无关"——自定义连接的启动请求
	// 里 TenantID 天然是空的（它直连自己的 Base URL，不经租户）。若把空原样落盘，用一次
	// 自定义模型就会把已选租户抹掉，多租户用户下次启动便要重新选一遍。
	if strings.TrimSpace(req.TenantID) == "" {
		req.TenantID = prev.TenantID
	}
	req = protectRequestSecrets(req)
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return
	}
	// Atomic replace (temp + rename): a crash mid-write must never leave a torn
	// config holding half of the encrypted credentials or the MRU list.
	_ = writeFileAtomic(filepath.Dir(path), filepath.Base(path), data)
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
	req, _ := loadRawConfigOK()
	return req
}

// loadRawConfigOK is loadRawConfig plus "was there anything to read". The flag
// matters where a zero value is a real setting rather than "unset": carrying a
// missing file's zeros forward would silently clear a seeded default.
func loadRawConfigOK() (StartSessionRequest, bool) {
	path, err := desktopConfigPath()
	if err != nil {
		return StartSessionRequest{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return StartSessionRequest{}, false
	}
	var req StartSessionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return StartSessionRequest{}, false
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

// updateRawConfig applies mutate to a fresh read of the persisted request and
// writes the result back atomically, all under configMu. It is the only way to
// change individual persisted fields (custom models, web proxy, tenant): callers
// that did their own load→mutate→save would race other writers and lose updates.
// Validation that depends on the current snapshot belongs in mutate; returning an
// error aborts without writing. Persistence failures are returned so settings UIs
// never report success for a change that did not reach disk.
func updateRawConfig(mutate func(*StartSessionRequest) error) error {
	configMu.Lock()
	defer configMu.Unlock()
	cfg := loadRawConfig()
	if err := mutate(&cfg); err != nil {
		return err
	}
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
