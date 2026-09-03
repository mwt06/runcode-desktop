//go:build !windows

package desktop

// RunUpdateWatch: 非 Windows 上不存在看门模式——那边根本不由应用接管安装
// （见 update_other.go 的 canLaunchInstaller），也就没有"应用退出之后谁把它拉回来"
// 这个问题。
//
// 仍然给出一个实现，是为了让 cmd/runcode-desktop/main.go 里那句入口判断不必带
// 平台条件编译：入口只有一处、三平台同一份，比在 main 里再分一次叉好读。这里永远
// 不会被走到（IsUpdateWatch 只有本包自己在 Windows 上拼出来的参数才为真）。
func RunUpdateWatch([]string) int { return 0 }
