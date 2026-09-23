//go:build !windows

package desktop

// RunUpdateWatch 在非 Windows 上是空实现；Linux/macOS 的重启用各自的脱离进程
// 脚本等待退出，不使用 Windows 的 NSIS 看门模式。
func RunUpdateWatch([]string) int { return 0 }
