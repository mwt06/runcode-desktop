//go:build linux

package desktop

// Linux 的 Secret Service 读写，走 libsecret 的 secret-tool 命令。
//
// Secret Service 是 freedesktop 的标准 D-Bus 接口，gnome-keyring 与 kwallet 都实现
// 它；银河麒麟的桌面环境基于 UKUI（GNOME 血统），带的是 gnome-keyring。
//
// 为什么用命令行而不是直接接 D-Bus：接 D-Bus 要引入一个第三方库并自己处理会话总线
// 的连接、解锁、prompt 交互，而这里只有「取一把主密钥」这一个用途。secret-tool 是
// libsecret 自带的官方工具，把那些都办掉了。
//
// **它不一定装得上**：secret-tool 来自 libsecret-tools 包，而 Secret Service 本身
// 还要求有一个在跑的钥匙串守护进程（无桌面会话的服务器上就没有）。取不到时上层
// 一律走 ok=false，凭据不落盘、让用户重新输入——把密钥和密文一起明文放在同一台
// 机器上，安全性并不比不存更好。

import (
	"os/exec"
	"strings"
)

// keyringGet 查一条密码。查不到时 secret-tool 以非零退出。
func keyringGet(service, account string) (string, bool) {
	out, err := exec.Command("secret-tool", "lookup",
		"service", service, "account", account).Output()
	if err != nil {
		return "", false
	}
	// lookup 不带结尾换行，但空结果也是「成功退出 + 空输出」，要当作没有。
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", false
	}
	return v, true
}

// keyringSet 存一条密码。
//
// secret-tool store 从 **stdin** 读密码，所以密钥不会出现在进程命令行里
// （ps 看不到）——这一点比 macOS 那边的 security 好，那个只能用参数传。
func keyringSet(service, account, secret string) bool {
	cmd := exec.Command("secret-tool", "store",
		"--label=runcode desktop master key",
		"service", service, "account", account)
	cmd.Stdin = strings.NewReader(secret)
	return cmd.Run() == nil
}
