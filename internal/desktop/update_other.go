//go:build !windows && !linux

package desktop

import "errors"

// canLaunchInstaller: macOS 上不由应用接管安装。
//
// macOS 的分发形态是 .app（打成 zip）。替换一个**正在运行的** .app 牵扯到签名、
// 公证票据与隔离属性，做错了的表现是「装完打不开、还被 Gatekeeper 拦下」，比不做
// 糟得多。所以那边只下载并校验，然后把用户送到包所在的文件夹（见 InstallUpdate），
// 由他自己拖进「应用程序」——这一步 macOS 用户本来就熟。
//
// 要消除这个差异，得先有稳定的签名与公证链路（CLAUDE.md 里 macOS 打包那一节）。
func canLaunchInstaller() bool { return false }

// willAutoRestart: 不由应用接管安装，谈不上自动重启。
func willAutoRestart() bool { return false }

// installHint：界面在它旁边放着「打开安装包所在文件夹」，所以说的是打开之后怎么做。
func installHint() string { return "把新版本拖进「应用程序」覆盖旧版即可" }

// runInstaller 不会被调到（canLaunchInstaller 恒为假），留着只为编译。
func (a *App) runInstaller(string, string) error {
	return errors.New("本平台不支持由应用直接安装更新")
}

// cleanStaleWatchers: 没有看门进程，也就没有它的副本要清。
func cleanStaleWatchers() {}
