//go:build linux

package desktop

// Linux 上「钥匙串为什么用不了」的分诊。三条分支按**排查顺序**排，每一条都给一句
// 能照抄的命令——这三种是 2026-09-18 在麒麟 V10 SP1 上实际走过的那条路。

import (
	"os"
	"os/exec"
)

func secretHint() (reason, fix string) {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return "系统缺少 secret-tool 命令，无法访问系统钥匙串（它来自 libsecret-tools 包）。",
			"sudo apt install libsecret-tools libpam-gnome-keyring，然后注销并重新登录一次图形界面。"
	}
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		return "当前会话没有 D-Bus 会话总线，系统钥匙串不可达（远程登录或无桌面会话时会这样）。",
			"请在图形桌面会话里运行本应用。"
	}
	// secret-tool 在、总线也在，那多半是登录钥匙串压根没被创建。麒麟等发行版的
	// gnome-keyring 包不含 PAM 模块，于是登录时没有任何东西去创建并解锁它，而
	// /etc/pam.d/ 里引用该模块的行带 `-` 前缀，缺失时**静默跳过**、不留任何痕迹。
	return "系统钥匙串不可用：登录钥匙串可能尚未创建或未解锁。部分发行版（如银河麒麟）的 gnome-keyring 不含 PAM 模块，登录时不会自动创建它。",
		"sudo apt install libpam-gnome-keyring，然后注销并重新登录一次图形界面。"
}
