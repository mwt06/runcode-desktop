package desktop

import "github.com/wt68/runcode/internal/protocol"

// desktopConfig 是兼容旧 JSON 的私有存储模型，不是前端命令。
type desktopConfig struct {
	ModelContexts   []protocol.ModelContextPreference `json:"modelContexts,omitempty"`
	Vision          protocol.VisionSettings           `json:"vision,omitempty"`
	VisionRevision  uint64                            `json:"visionRevision,omitempty"`
	CWD             string                            `json:"cwd"`
	Provider        string                            `json:"provider"`
	Model           string                            `json:"model"`
	CustomModelName string                            `json:"customModelName,omitempty"`
	TenantID        string                            `json:"tenantId"`
	BaseURL         string                            `json:"baseURL"`
	APIKey          string                            `json:"apiKey"`
	AuthToken       string                            `json:"authToken"`
	// 加密列只在后端落盘/解密，公共视图没有对应字段。
	APIKeyProtected    string `json:"apiKeyProtected,omitempty"`
	AuthTokenProtected string `json:"authTokenProtected,omitempty"`
	PermissionMode     string `json:"permissionMode"`
	ReasoningScenario  string `json:"reasoningScenario"`
	ThinkingEffort     string `json:"thinkingEffort"`
	HarmJudgeModel     string `json:"harmJudgeModel"`
	HarmJudgeVotes     int    `json:"harmJudgeVotes"`
	Resume             string `json:"resume"`
	Continue           bool   `json:"continue"`
	// 这些字段不属于启动请求，启动保存必须在原配置上保留它们。
	RecentWorkspaces []string      `json:"recentWorkspaces,omitempty"`
	CustomModels     []CustomModel `json:"customModels,omitempty"`
	WebProxy         string        `json:"webProxy,omitempty"`
	SkipLogin        bool          `json:"skipLogin,omitempty"`
	ContextAudit     bool          `json:"contextAudit,omitempty"`
	// 文生图默认模型，按账号/Bridge/租户分区（键是 imageGenerationScope）。只经
	// SaveImageGenerationSettings 修改；其他保存路径从原配置出发，自然保留它。
	ImageGenerationPreferences map[string]imageGenerationPreference `json:"imageGenerationPreferences,omitempty"`
	// 邮箱账号只经 SaveMailAccount / Disable / Remove 修改；密码密文不进 SettingsView。
	MailAccount *mailAccountRecord `json:"mailAccount,omitempty"`
}

// mailAccountRecord 是 desktop.json 私有存储；PasswordProtected 经 DPAPI/钥匙串保护。
type mailAccountRecord struct {
	ID                   string `json:"id"`
	Address              string `json:"address"`
	DisplayName          string `json:"displayName"`
	Version              string `json:"version"`
	Host                 string `json:"host"`
	Port                 int    `json:"port"`
	PasswordProtected    string `json:"passwordProtected,omitempty"`
	Enabled              bool   `json:"enabled"`
	Revision             int64  `json:"revision"`
	CredentialRevision   int64  `json:"credentialRevision"`
	OnboardingStatus     string `json:"onboardingStatus"`
	LastConnectionTestAt string `json:"lastConnectionTestAt,omitempty"`
	LastTestFingerprint  string `json:"lastTestFingerprint,omitempty"`
	TestMailAcceptedAt   string `json:"testMailAcceptedAt,omitempty"`
	ReceiptConfirmedAt   string `json:"receiptConfirmedAt,omitempty"`
	LastTestShortID      string `json:"lastTestShortId,omitempty"`
	LastTestSubmissionID string `json:"lastTestSubmissionId,omitempty"`
}

func configFromRequest(req StartSessionRequest) desktopConfig {
	return desktopConfig{
		CWD:               req.CWD,
		Provider:          req.Provider,
		Model:             req.Model,
		CustomModelName:   req.CustomModelName,
		TenantID:          req.TenantID,
		BaseURL:           req.BaseURL,
		APIKey:            req.APIKey,
		AuthToken:         req.AuthToken,
		PermissionMode:    req.PermissionMode,
		ReasoningScenario: req.ReasoningScenario,
		ThinkingEffort:    req.ThinkingEffort,
		HarmJudgeModel:    req.HarmJudgeModel,
		HarmJudgeVotes:    req.HarmJudgeVotes,
		Resume:            req.Resume,
		Continue:          req.Continue,
	}
}

// startRequest 仅供后端接线，凭据解密不经过前端。
func (cfg desktopConfig) startRequest() StartSessionRequest {
	cfg = unprotectConfigSecrets(cfg)
	return StartSessionRequest{
		CWD:               cfg.CWD,
		Provider:          cfg.Provider,
		Model:             cfg.Model,
		CustomModelName:   cfg.CustomModelName,
		TenantID:          cfg.TenantID,
		BaseURL:           cfg.BaseURL,
		APIKey:            cfg.APIKey,
		AuthToken:         cfg.AuthToken,
		PermissionMode:    cfg.PermissionMode,
		ReasoningScenario: cfg.ReasoningScenario,
		ThinkingEffort:    cfg.ThinkingEffort,
		HarmJudgeModel:    cfg.HarmJudgeModel,
		HarmJudgeVotes:    cfg.HarmJudgeVotes,
		Resume:            cfg.Resume,
		Continue:          cfg.Continue,
	}
}

func (cfg desktopConfig) view() SettingsView {
	return SettingsView{
		CWD:               cfg.CWD,
		Provider:          cfg.Provider,
		Model:             cfg.Model,
		CustomModelName:   cfg.CustomModelName,
		TenantID:          cfg.TenantID,
		BaseURL:           cfg.BaseURL,
		PermissionMode:    cfg.PermissionMode,
		ReasoningScenario: cfg.ReasoningScenario,
		ThinkingEffort:    cfg.ThinkingEffort,
		HarmJudgeModel:    cfg.HarmJudgeModel,
		HarmJudgeVotes:    cfg.HarmJudgeVotes,
		RecentWorkspaces:  cfg.RecentWorkspaces,
		WebProxy:          cfg.WebProxy,
		SkipLogin:         cfg.SkipLogin,
		ContextAudit:      cfg.ContextAudit,
	}
}
