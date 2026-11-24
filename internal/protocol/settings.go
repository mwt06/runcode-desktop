package protocol

// ErrCodeSettingsApplyFailed 表示设置已经成功落盘，但应用到指定活跃会话时失败。
// 前端据此区分“完全没保存”和“配置已保存、当前会话尚未应用”。
const ErrCodeSettingsApplyFailed = "settings_apply_failed"

// SaveSettingsRequest 只包含设置页拥有的字段，连接与租户由专用命令管理。
type SaveSettingsRequest struct {
	// SessionID 区分旧调用与显式寻址：nil 保留旧的聚焦会话语义；空串只写默认配置；
	// 非空只作用于该会话。自动保存必须显式传值，避免排队期间焦点切换后写错会话。
	SessionID      *string `json:"sessionId,omitempty"`
	PermissionMode string  `json:"permissionMode"`
	HarmJudgeModel string  `json:"harmJudgeModel"`
	HarmJudgeVotes int     `json:"harmJudgeVotes"`
	SkipLogin      bool    `json:"skipLogin,omitempty"`
}

// SettingsView 是脱敏只读视图，不包含凭据或存储记录。
type SettingsView struct {
	CWD               string   `json:"cwd"`
	Provider          string   `json:"provider"`
	Model             string   `json:"model"`
	CustomModelName   string   `json:"customModelName,omitempty"`
	TenantID          string   `json:"tenantId"`
	BaseURL           string   `json:"baseURL"`
	PermissionMode    string   `json:"permissionMode"`
	ReasoningScenario string   `json:"reasoningScenario"`
	ThinkingEffort    string   `json:"thinkingEffort"`
	HarmJudgeModel    string   `json:"harmJudgeModel"`
	HarmJudgeVotes    int      `json:"harmJudgeVotes"`
	RecentWorkspaces  []string `json:"recentWorkspaces,omitempty"`
	WebProxy          string   `json:"webProxy,omitempty"`
	SkipLogin         bool     `json:"skipLogin,omitempty"`
	ContextAudit      bool     `json:"contextAudit,omitempty"`
}
