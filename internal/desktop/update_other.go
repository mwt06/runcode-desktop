//go:build !windows

package desktop

import (
	"errors"
	"runtime"
)

// canLaunchInstaller: 非 Windows 上不由应用接管安装。
//
// macOS 的分发形态是 .app（打成 zip）。替换一个**正在运行的** .app 牵扯到签名、
// 公证票据与隔离属性，做错了的表现是「装完打不开、还被 Gatekeeper 拦下」，比不做
// 糟得多。所以那边只下载并校验，然后把用户送到包所在的文件夹（见 InstallUpdate），
// 由他自己拖进「应用程序」——这一步 macOS 用户本来就熟。
//
// 要消除这个差异，得先有稳定的签名与公证链路（CLAUDE.md 里 macOS 打包那一节）。
func canLaunchInstaller() bool { return false }

// willAutoRestart: 非 Windows 上根本不由应用接管安装，谈不上自动重启。
func willAutoRestart() bool { return false }

// manualInstallHint 是下好之后、用户自己动手装的那一句说明。界面在它旁边放着
// 「打开安装包所在文件夹」，所以说的是打开之后怎么做。
//
// Linux 上没有接管安装：装 deb 要 root，而这一步交给系统的软件包安装器（麒麟上是
// kylin-installer，双击 deb 即可），密码框、依赖检查与安全中心的来源检查都由它按
// 系统的规矩走，比我们自己去跑一趟 sudo dpkg 可靠得多。装完要重开应用，因为正在
// 跑的这个进程还是旧的二进制。
func manualInstallHint() string {
	if runtime.GOOS == "darwin" {
		return "把新版本拖进「应用程序」覆盖旧版即可"
	}
	return "双击其中的安装包，用系统的软件包安装器装好后，重新打开本应用"
}

func launchInstaller(string, string) error {
	return errors.New("本平台不支持由应用直接安装更新")
}

// cleanStaleWatchers: 非 Windows 上没有看门进程，也就没有它的副本要清。
func cleanStaleWatchers() {}
