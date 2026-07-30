package protocol

// ContextAuditInfo 是"上下文审核"（仅测试版构建存在）的状态快照。开启后，引擎每次
// 发给模型的完整请求上下文（系统提示词、全部消息历史、工具清单）都会落盘，并由一个
// 仅监听 127.0.0.1 的本地页面提供查看。
type ContextAuditInfo struct {
	// Supported 报告本构建是否为测试版（ldflags 注入）。false 时功能整体不存在：
	// 设置页不渲染区块，SetContextAudit 也会拒绝开启。
	Supported bool `json:"supported"`
	// Enabled 是开关的当前值（持久化在 desktop.json，后端所有）。
	Enabled bool `json:"enabled"`
	// URL 是查看页面的地址（http://127.0.0.1:<端口>/），仅开启且服务器在运行时非空。
	URL string `json:"url"`
	// Dir 是审核记录的落盘目录，供用户直接检查原始 JSONL。
	Dir string `json:"dir"`
}
