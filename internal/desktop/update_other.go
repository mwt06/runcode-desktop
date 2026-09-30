//go:build !windows && !linux && !darwin

package desktop

import "errors"

// 未实现的平台只支持下载与手动安装。
func canLaunchInstaller() bool { return false }

// willAutoRestart: 不由应用接管安装，谈不上自动重启。
func willAutoRestart() bool { return false }

// installHint：界面在它旁边放着「打开安装包所在文件夹」，所以说的是打开之后怎么做。
func installHint() string { return "请手动安装下载好的更新包" }

// runInstaller 不会被调到（canLaunchInstaller 恒为假），留着只为编译。
func (a *App) runInstaller(string, string, string) error {
	return errors.New("本平台不支持由应用直接安装更新")
}

// cleanStaleWatchers: 没有看门进程，也就没有它的副本要清。
func cleanStaleWatchers() {}
