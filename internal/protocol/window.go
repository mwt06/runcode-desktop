package protocol

// WindowCapabilitiesView 是宿主窗口能力只读快照；不查自动化任务库。
type WindowCapabilitiesView struct {
	Available           bool `json:"available"`
	BackgroundSupported bool `json:"backgroundSupported"`
}
