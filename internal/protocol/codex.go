package protocol

// ChatGPT(Codex)登录的 wire 类型。走的是设备码流程：先取一串码给用户，用户在
// 浏览器里输完，客户端才换到令牌——所以它天然是两步，命令面也是两条
// (CodexStartLogin / CodexAwaitLogin)，而不像通行证那样一条命令从头等到尾。

// CodexDeviceCode 是发起登录后要展示给用户的东西：把 UserCode 显示出来，让他在
// VerificationURL 那个页面里输入。ExpiresAt 之后这串码作废，要重新发起。
type CodexDeviceCode struct {
	UserCode        string `json:"userCode"`
	VerificationURL string `json:"verificationUrl"`
	// ExpiresAt 是 RFC3339 时间串，前端据此显示倒计时。
	ExpiresAt string `json:"expiresAt"`
}

// CodexStatus 是 ChatGPT 账号的登录态。未登录时 LoggedIn=false，其余字段为空。
type CodexStatus struct {
	LoggedIn bool `json:"loggedIn"`
	// Email 来自 id_token，仅用于界面上显示"当前登录的是谁"，可能为空。
	Email string `json:"email,omitempty"`
	// AccountID 是上游要的 chatgpt-account-id。回给前端只为排查问题时能看到，
	// 它不是密钥(真正的凭据是不出后端的令牌)。
	AccountID string `json:"accountId,omitempty"`
}

// CodexModel 是 ChatGPT 账号下可用的一个 Codex 模型。清单由上游给出，不是本地
// 写死的表——可用模型按账号与订阅档次变化。
type CodexModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
}
