package protocol

// PassportStatus 是前端展示用的登录态 + 用户信息。
type PassportStatus struct {
	LoggedIn bool   `json:"loggedIn"`
	UserID   string `json:"userId,omitempty"`
	UserName string `json:"userName,omitempty"`
	Name     string `json:"name,omitempty"`
	Nickname string `json:"nickname,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
	TenantID string `json:"tenantId,omitempty"`
}

// PassportModel 是 Bridge /v1/models 列表项。
//
// Local 及其后两个字段是本产品对 OpenAI 模型清单的扩展，由基座的 bridge.catalog
// 下发（改 ConfigMap 即生效，客户端不必发版）:
//
//   - Local 标记该模型的推理在内网完成。**客户端只认这个标记、不做二次判断**——
//     "这个 id 的渠道到底指向哪里"只有基座知道，把它写死在客户端等于让两处事实
//     互相漂移。标记错了的后果是保密数据出内网，所以基座那边要在配置旁写明依据。
//   - LocalDefault 指定 Local 模型里默认用哪一个。只标 Local 不够:清单顺序一变
//     就换了模型，而"换哪个模型"必须是显式声明。
//   - ContextTokens 是该模型的上下文窗口。本地模型的窗口通常远小于云端模型，
//     切过去时要按它重设会话预算，否则历史一长第一次请求就超限。0 = 未声明，
//     沿用会话原值。
type PassportModel struct {
	ID            string `json:"id"`
	OwnedBy       string `json:"ownedBy"`
	Local         bool   `json:"local,omitempty"`
	LocalDefault  bool   `json:"localDefault,omitempty"`
	ContextTokens int    `json:"contextTokens,omitempty"`
}

// PassportTenant 是当前用户可用的租户（Bridge /api/tenants）。
// ParentID 供前端渲染层级树；父租户可能不在用户的可用列表里
// （用户只被授了子级），前端须把这类节点当根渲染，不能丢弃。
type PassportTenant struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parentId,omitempty"`
}

// SecretStorage 报告本机能不能安全保存登录状态。
//
// 凭据保护在取不到系统钥匙串时**故意不落盘**（理由见 desktop/secret_keyring.go）。
// 这个取舍是对的，但此前它是全静默的——用户看到的只有"怎么每次都要重新登录"，
// 而真正的原因可能只是少装了一个包。这三个字段就是为了把那句话说出来。
type SecretStorage struct {
	// OK 为真表示登录状态能跨重启保留；为假时下面两句说明原因与修法。
	OK bool `json:"ok"`
	// Reason 是为什么存不住，一句人话。
	Reason string `json:"reason,omitempty"`
	// Fix 是可以照抄的修法（通常是一条命令）；没有可行修法时为空。
	Fix string `json:"fix,omitempty"`
}
