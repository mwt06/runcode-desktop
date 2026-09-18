//go:build !linux && !darwin

package desktop

// Windows 与其余平台。
//
// Windows 走 DPAPI，它不依赖任何守护进程或图形会话，正常情况下不会失败——真失败了
// 也没有用户能照做的修法，所以这里只说清楚后果，不给一条假的建议。
func secretHint() (reason, fix string) {
	return "本机的凭据加密不可用，登录状态无法保存。", ""
}
