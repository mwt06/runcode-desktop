//go:build !windows

package desktop

import (
	"os"
	"runtime"
)

// macOS 没有 DISPLAY，模型必须显式传 -A；不伪造 DISPLAY 影响其他 GUI 工具。
func askpassAvailable() bool { return runtime.GOOS == "darwin" || os.Getenv("DISPLAY") != "" }

func (a *App) privilegeReady() bool { return a.askpassReady() }

func (a *App) platformPrivilegePrompt() string {
	if !a.privilegeReady() {
		return ""
	}
	prompt := privilegePrompt()
	if runtime.GOOS == "darwin" {
		prompt += "On macOS, always use `/usr/bin/sudo -A -k <command>`: -A selects the application password dialog without a terminal, and -k requires fresh authorization. Do not use sudo -S or ask for a password in chat. Homebrew itself must run as the regular user, not under sudo.\n"
	}
	return prompt
}

// ---- 提示词 --------------------------------------------------------------

// privilegePrompt 告诉模型 sudo 能用、以及它的代价。只陈述事实，不下禁令：模型自己
// 判断值不值得为这件事打扰用户两次。
func privilegePrompt() string {
	return "## Administrator (sudo) commands\n\n" +
		"This reflects the current state of this app and supersedes older notes (for example in memory) saying sudo is refused. " +
		"`sudo` works in this app, but every sudo command is shown to the user for approval, " +
		"and the user then types their system password in a dialog — the password never reaches you. " +
		"Each sudo command interrupts the user twice, so use it only when a task genuinely needs system-level changes " +
		"(for example installing a system package). Commands that delete files or write to disks " +
		"(rm, dd, mkfs, …) are refused under sudo, as are pkexec, su and doas.\n"
}
