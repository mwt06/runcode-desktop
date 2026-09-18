package desktop

// 「本机能不能安全保存登录状态」的自述。
//
// 为什么需要这个：凭据保护在取不到系统钥匙串时**故意不落盘**（宁可让用户重新登录，
// 也不把密钥和密文一起明文放在同一台机器上，见 secret_keyring.go）。这个取舍本身是
// 对的，但它此前是**全静默**的——persistTokens 直接 return，不写文件、不记日志、不
// 告诉界面。用户看到的只有"怎么每次都要重新登录"。
//
// 这条静默有多难查，2026-09-18 在一台麒麟 V10 SP1 上有过一次完整的实例：
//
//  1. 麒麟的 gnome-keyring 包**只带守护进程、不带 PAM 模块**（模块在单独的
//     libpam-gnome-keyring 包里）。
//  2. /etc/pam.d/lightdm 里引用它的两行带 `-` 前缀 —— 模块缺失就静默跳过。
//  3. 于是登录钥匙串从未被创建；D-Bus 按需拉起的守护进程手里没有登录密码，
//     只能弹窗要人新建，无人值守时就一直挂着。
//  4. 我们 5 秒超时后当作"取不到钥匙串"，静默不落盘。
//
// 四层静默叠在一起，从现象根本回溯不到第 1 层。所以这里把结论变成一句人话加一条
// 可照抄的命令——修一次的成本是几十秒，查一次的成本是半天。

import (
	"sync"

	"github.com/wt68/runcode/internal/protocol"
)

// secretStorage 是本机凭据存储的状态。
type secretStorage struct {
	ok          bool
	reason, fix string
}

// secretProbe 是探测用的哨兵值。内容不重要，只是要走一遍真实的加密路径——
// 只检查"命令在不在"会漏掉"命令在但钥匙串锁着"这一类，而那正是最常见的一种。
const secretProbe = "runcode-secret-probe"

// secretStatusOnce 缓存探测结果。
//
// 结果在进程生命周期内不会变（masterKeyOnce 本身也只取一次），而每次都探一遍意味着
// 每次都跑一个外部命令。**代价是用户装完包之后要重启应用才看得到状态变化**——所以
// 下面每条 fix 文案都以"重新登录/重启"收尾，那本来也是 PAM 生效的必要条件。
var secretStatusOnce = sync.OnceValue(func() secretStorage {
	if _, ok := protectSecret(secretProbe); ok {
		return secretStorage{ok: true}
	}
	reason, fix := secretHint()
	return secretStorage{reason: reason, fix: fix}
})

// secretStorageStatus 报告本机能不能安全保存登录状态。
func secretStorageStatus() secretStorage { return secretStatusOnce() }

// SecretStorageStatus 告诉前端本机能不能安全保存登录状态（纯读，结果有缓存）。
func (a *App) SecretStorageStatus() protocol.SecretStorage {
	s := secretStorageStatus()
	return protocol.SecretStorage{OK: s.ok, Reason: s.reason, Fix: s.fix}
}
