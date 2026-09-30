package protocol

// SaveSettingsRequest 只包含设置页拥有的字段，连接与租户由专用命令管理。
type SaveSettingsRequest struct {
	PermissionMode     string `json:"permissionMode"`
	HarmJudgeModel     string `json:"harmJudgeModel"`
	HarmJudgeVotes     int    `json:"harmJudgeVotes"`
	MaxTokens          int    `json:"maxTokens"`
	MaxContextTokens   int    `json:"maxContextTokens"`
	MaxHistoryMessages int    `json:"maxHistoryMessages"`
	SkipLogin          bool   `json:"skipLogin,omitempty"`
}

// SettingsView 是脱敏只读视图，不包含凭据或存储记录。
type SettingsView struct {
	CWD                string   `json:"cwd"`
	Provider           string   `json:"provider"`
	Model              string   `json:"model"`
	CustomModelName    string   `json:"customModelName,omitempty"`
	TenantID           string   `json:"tenantId"`
	BaseURL            string   `json:"baseURL"`
	PermissionMode     string   `json:"permissionMode"`
	ReasoningScenario  string   `json:"reasoningScenario"`
	ThinkingEffort     string   `json:"thinkingEffort"`
	HarmJudgeModel     string   `json:"harmJudgeModel"`
	HarmJudgeVotes     int      `json:"harmJudgeVotes"`
	MaxTokens          int      `json:"maxTokens"`
	MaxContextTokens   int      `json:"maxContextTokens"`
	MaxHistoryMessages int      `json:"maxHistoryMessages"`
	RecentWorkspaces   []string `json:"recentWorkspaces,omitempty"`
	WebProxy           string   `json:"webProxy,omitempty"`
	SkipLogin          bool     `json:"skipLogin,omitempty"`
	ContextAudit       bool     `json:"contextAudit,omitempty"`
}
