package desktop

// Linux 上由应用自己装更新时，与平台无关、可以在任何机器上测的那一半：拼 apt 的
// 命令行、把 apt/sudo 的报错翻成人话。
// 真正执行它们的是 update_linux.go。

import (
	"errors"
	"fmt"
	"strings"
)

// aptLockWait 是等 dpkg 锁的秒数。麒麟开机后「软件更新」常在后台跑 apt，撞上了
// 与其当场报错，不如等它一会儿——用户看到的是「正在安装」多转一阵，而不是一句
// 他什么也做不了的报错。
const aptLockWait = 120

// aptInstallArgs 是交给 sudo 的参数。
//
// 用 apt-get 而不是 dpkg -i：新版本多了依赖时 dpkg 直接失败，apt 会一并装上；
// nfpm 的 Recommends（钥匙串那两个包，见 CLAUDE.md）也只有 apt 会装。
// file 必须是绝对路径——apt 靠开头的 / 或 ./ 才认出这是本地文件而不是包名。
//
// `-A` 让 sudo 走 SUDO_ASKPASS（本应用的密码框），理由同 kysecTrust：从终端启动时
// sudo 会去终端问密码，应用的密码框永远等不到请求。
func aptInstallArgs(file string) []string {
	return []string{
		"-A", "apt-get", "install", "-y",
		"-o", fmt.Sprintf("DPkg::Lock::Timeout=%d", aptLockWait),
		file,
	}
}

// aptInstallError 把 sudo/apt 的输出翻成一句用户能照着做的话。
//
// 认不出来的原样带上 apt 的最后几行：安装失败的原因千奇百怪（源不通、依赖冲突、
// 安全中心拦截），与其猜错，不如把原话给出来——那也是用户报给管理员时最有用的东西。
func aptInstallError(out string, err error) error {
	switch {
	case strings.Contains(out, "no password was provided"), strings.Contains(out, errAskpassCanceled.Error()):
		return errors.New("已取消授权，更新没有安装。随时可以再点「立即安装并重启」")
	case strings.Contains(out, "incorrect password"), strings.Contains(out, "密码不正确"):
		return errors.New("密码不正确，更新没有安装，请重试")
	case strings.Contains(out, "not in the sudoers"), strings.Contains(out, "不在 sudoers"):
		return errors.New("当前用户没有管理员权限（不在 sudoers 中），无法安装更新，请联系管理员")
	case strings.Contains(out, "Could not get lock"), strings.Contains(out, "Unable to acquire the dpkg frontend lock"),
		strings.Contains(out, "无法获得锁"):
		return errors.New("系统正在安装或更新别的软件，请等它结束后再试")
	}
	tail := lastLines(out, 3)
	if tail == "" && err != nil {
		tail = err.Error()
	}
	return fmt.Errorf("安装更新失败：%s", tail)
}

// lastLines 取 s 的最后 n 个非空行，用「；」连起来。
func lastLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "；")
}
