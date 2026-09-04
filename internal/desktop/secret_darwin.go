//go:build darwin

package desktop

// macOS 的钥匙串读写，走系统自带的 security 命令。
//
// 为什么不用 CGO 直接调 Security.framework：那需要 cgo 才能编，而本仓的 CLI 与
// 服务端骨架都要保持能在 CGO_ENABLED=0 下构建；桌面外壳虽然开着 cgo，但为一个
// 只在登录时调用几次的操作引入 Objective-C 桥接，换来的复杂度远大于省下的那点
// 进程开销。security 是系统组件，macOS 上必然存在。

import (
	"os/exec"
	"strings"
)

// keyringGet 读一条通用密码。找不到时 security 以非零退出，视作「没有」。
func keyringGet(service, account string) (string, bool) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", service, "-a", account, "-w").Output()
	if err != nil {
		return "", false
	}
	// -w 只输出密码本身，但结尾带换行。
	return strings.TrimSpace(string(out)), true
}

// keyringSet 写一条通用密码。
//
// -U 是「已存在就更新」：不加它时 security 对同一个 service/account 会以
// "The specified item already exists in the keychain" 失败，而不是覆盖。
//
// 密码经 -w 参数传递，会短暂出现在进程命令行里（ps 看得到）。这里可以接受：写的是
// 一把随机主密钥而不是用户凭据本身，且只在首次运行时发生一次。要彻底避免得用
// stdin，而 security 的 add-generic-password 不从 stdin 读密码。
func keyringSet(service, account, secret string) bool {
	err := exec.Command("security", "add-generic-password",
		"-s", service, "-a", account, "-w", secret,
		"-U",     // update if exists
		"-T", "", // 不预授权任何程序访问，读取时按系统策略走
	).Run()
	return err == nil
}
