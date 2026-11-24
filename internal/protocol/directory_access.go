package protocol

// DirectoryAccessRequest 指名调用会话，从不猜焦点会话。
type DirectoryAccessRequest struct {
	ConfirmRoot bool   `json:"confirmRoot,omitempty"`
	SessionID   string `json:"sessionId"`
	Directory   string `json:"directory"`
	Access      string `json:"access"`
	Scope       string `json:"scope"`
}

// DirectoryAccessGrant 是一条作用域内的目录允许规则。
type DirectoryAccessGrant struct {
	Directory string   `json:"directory"`
	Access    string   `json:"access"`
	Scope     string   `json:"scope"`
	CoveredBy []string `json:"coveredBy"`
}

// DirectoryAccessView 列出指定会话的目录规则。
type DirectoryAccessView struct {
	SessionID string                 `json:"sessionId"`
	Workspace string                 `json:"workspace"`
	Revision  string                 `json:"revision"`
	Grants    []DirectoryAccessGrant `json:"grants"`
}

// ArtifactReference 是有界、绑定会话实例的文件能力句柄。
type ArtifactReference struct {
	Reference  string `json:"reference"`
	SessionID  string `json:"sessionId"`
	Generation string `json:"generation"`
	Path       string `json:"path"`
	URL        string `json:"url"`
}

// DirectoryAccessChanged 使某一工作区的目录视图失效。
type DirectoryAccessChanged struct {
	Workspace string `json:"workspace"`
}

// PreviewLifecycle 让桌面文件预览缓存与会话实例重建对齐。
// Phase 为 invalidating／ready／closed。ready 携带新的 host generation；
// invalidating／closed 可将 Generation 留空。不覆盖聊天内静态 HTML 预览预算。
type PreviewLifecycle struct {
	SessionID  string `json:"sessionId"`
	Phase      string `json:"phase"`
	Generation string `json:"generation,omitempty"`
}
