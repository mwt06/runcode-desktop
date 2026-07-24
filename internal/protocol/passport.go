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
type PassportModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"ownedBy"`
}

// PassportTenant 是当前用户可用的租户（Bridge /api/tenants）。
// ParentID 供前端渲染层级树；父租户可能不在用户的可用列表里
// （用户只被授了子级），前端须把这类节点当根渲染，不能丢弃。
type PassportTenant struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parentId,omitempty"`
}
