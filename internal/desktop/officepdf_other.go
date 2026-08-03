//go:build !windows

package desktop

// 非 Windows 平台没有这条高保真通路:转换靠的是 COM 自动化,那是 Windows 独有的。
// macOS 上 Keynote/Pages 的 AppleScript 与 LibreOffice 的 --convert-to 都能做同样的
// 事,但各有各的安装前提,值得单独一轮来做,而不是在这里塞一个未经验证的分支。
//
// 返回"不可用"而不是错误文案:前端据此静默退回内嵌的 JS 渲染器,与"装了 Office 但
// 这次转换失败"是同一条降级路径。
func convertOfficeToPDF(_, _ string) error {
	return errOfficeUnavailable
}
