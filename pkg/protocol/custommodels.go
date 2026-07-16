package protocol

// CustomModel 是用户自定义的直连模型接入点（OpenAI 兼容），与通行证平台模型
// 并列显示在模型选择器里；选中后按老的直连路径起会话。
type CustomModel struct {
	Name    string `json:"name"`
	Model   string `json:"model"`
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey,omitempty"`
	// APIKeyProtected 是 DPAPI 加密后的落盘形态（同 StartSessionRequest 的凭证）。
	APIKeyProtected string `json:"apiKeyProtected,omitempty"`
}
