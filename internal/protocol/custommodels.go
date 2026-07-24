package protocol

// CustomModel 是用户自定义的直连模型接入点，与通行证平台模型并列显示在模型
// 选择器里。Provider 是引擎 provider registry 的名称（当前为 openai/
// openai-responses/anthropic），由 llm.IsRegistered 校验而非本仓库的名单；旧配置
// 没有该字段时由桌面端按 openai 兼容处理。密钥字段仅用于桌面端持久化，
// ListCustomModels 对外返回时必须清空，只通过 HasAPIKey 暴露是否已配置。
type CustomModel struct {
	Name      string `json:"name"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model"`
	BaseURL   string `json:"baseURL"`
	HasAPIKey bool   `json:"hasAPIKey,omitempty"`
	APIKey    string `json:"apiKey,omitempty"`
	// APIKeyProtected 是 DPAPI 加密后的落盘形态（同 StartSessionRequest 的凭证）。
	APIKeyProtected string `json:"apiKeyProtected,omitempty"`
}

// SaveCustomModelRequest 新增或修改一个自定义模型。编辑时 OriginalName 定位旧
// 记录；APIKey 留空表示保留旧密钥，ClearAPIKey 才显式清除，两者不能同时使用。
type SaveCustomModelRequest struct {
	OriginalName string `json:"originalName,omitempty"`
	Name         string `json:"name"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model"`
	BaseURL      string `json:"baseURL"`
	APIKey       string `json:"apiKey,omitempty"`
	ClearAPIKey  bool   `json:"clearAPIKey,omitempty"`
}
