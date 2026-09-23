//go:build windows

package desktop

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func askpassAvailable() bool { return false }

// 不运行 sudo 探测、不写注册表、更不替用户启用提权。只认系统目录里的原生 sudo。
// Enabled: 0=禁用，1=新窗口，2=禁用输入，3=内联。组织策略限制允许的最高模式。
// 与 microsoft/sudo 的 RegistryConfigProvider 一致：没有配置=禁用，没有策略=不限，
// 其余读取错误必须关闭能力，不能把“读不到策略”解释为“没有策略”。
func windowsSudo() (string, uint64) {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", 0
	}
	bin := filepath.Join(dir, "sudo.exe")
	if st, err := os.Stat(bin); err != nil || st.IsDir() {
		return "", 0
	}
	mode := sudoRegistryMode(`SOFTWARE\Microsoft\Windows\CurrentVersion\Sudo`, 0)
	policy := sudoRegistryMode(`SOFTWARE\Policies\Microsoft\Windows\Sudo`, 3)
	return bin, min(mode, policy)
}

func sudoRegistryMode(key string, missing uint64) uint64 {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return sudoModeValue(0, err, missing)
	}
	defer func() { _ = k.Close() }()
	n, kind, err := k.GetIntegerValue("Enabled")
	if err == nil && kind != registry.DWORD {
		return 0
	}
	return sudoModeValue(n, err, missing)
}

func sudoModeValue(n uint64, err error, missing uint64) uint64 {
	if errors.Is(err, registry.ErrNotExist) {
		return missing
	}
	if err != nil {
		return 0
	}
	return min(n, 3)
}

func (a *App) privilegeReady() bool { _, mode := windowsSudo(); return mode > 0 }

func (a *App) platformPrivilegePrompt() string {
	bin, mode := windowsSudo()
	return windowsPrivilegePrompt(bin, mode)
}

func windowsPrivilegePrompt(bin string, mode uint64) string {
	if mode == 0 {
		return "## Administrator commands on Windows\n\nWindows native sudo is unavailable or disabled. It requires Windows 11 24H2 or later and the user enabling Sudo in Windows Settings. Do not try to enable it yourself, collect passwords, or bypass it with runas/Start-Process -Verb RunAs. Tell the user when administrator access is needed.\n"
	}
	prompt := "## Administrator commands on Windows\n\nUse the Windows native sudo at `" + bin + "` (quote the path in cmd). Every sudo command requires approval in this app, followed by Windows UAC; never collect an administrator password. Approval is for this command only. This is Windows sudo, not Unix sudo: do not use -A, -S or -k. Use a real executable (e.g. cmd /c for shell builtins). Destructive disk/file commands and other elevation routes remain refused.\n"
	if mode == 1 {
		return prompt + "This machine only permits the new-window mode: use `sudo --new-window <executable> <args>`. Output/exit status may not be available to this tool; verify the actual result before claiming success. Do not change the system mode yourself.\n"
	}
	return prompt + "This machine permits connected output: use `sudo --disable-input <executable> <args>` so the elevated process cannot read input from this tool.\n"
}
