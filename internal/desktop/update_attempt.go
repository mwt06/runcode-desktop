package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 「上一次更新到底装成了没有」的记录。
//
// # 为什么需要它
//
// 更新这条链路上，判断"装成了"的信号全是**代理信号**，而代理信号都骗过人：
//
//   - 安装器退出了 —— 用户在 UAC 上点取消，它也退出。
//   - 应用 exe 的时间戳变新了 —— NSIS 解压时保留源文件时间戳，重装同一个构建时
//     它一动不动。
//   - 注册表 DisplayVersion 变成新版了 —— 实测出过一次"注册表 0.1.3、exe 还是
//     0.1.2"：静默模式下 NSIS 遇到写不动的文件是跳过并继续，注册表照写。
//
// 唯一不会骗人的判据是**应用自己报出来的版本**：它就是那个被跑起来的二进制。所以
// 拉起安装器之前先记下"这次要装成 X"，下次启动拿 X 和 AppVersion() 一比即可。
//
// # 它同时补上了"失败无声"这个窟窿
//
// 在这之前，更新失败的全部表现是：应用关掉、又回来了、版本没变、没有任何人说过
// 为什么。有了这条记录，界面能说出"上次更新到 X 未完成（当前仍是 Y）"。
type installAttempt struct {
	// Version 是这次要装成的版本。它取自**服务端清单**返回的 latest，不是任何
	// 构建期文件里的数——判断"装没装上"要拿运行中的二进制跟发布方声称的版本比，
	// 中间任何一层本地配置都只是转述。
	Version string `json:"version"`
	// From 是发起这次更新时正在跑的版本，进报错文案。
	From string `json:"from"`
	// At 是发起时刻（RFC3339），报错里要说清是什么时候的事。
	At string `json:"at"`
}

// attemptFile 是记录的落脚处：和安装包同一个缓存目录。
//
// 放缓存目录而不是配置目录，理由和安装包一样——它是一次性的过程状态，丢了最坏的
// 后果只是少报一句话，没有任何值得跟着漫游配置同步的东西。
func attemptFile() (string, error) {
	dir, err := updateCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "install-attempt.json"), nil
}

// writeInstallAttempt 在拉起安装器之前记下这一笔。
//
// 由**应用**写而不是由看门进程写，是有意的：看门进程可能压根没起来（被杀软拦下、
// 复制失败），而那恰恰是最需要报出来的一种失败。写在应用这边，只要用户还能打开
// 应用，这句话就一定说得出来。
func writeInstallAttempt(version string) error {
	path, err := attemptFile()
	if err != nil {
		return err
	}
	blob, err := json.Marshal(installAttempt{
		Version: strings.TrimSpace(version),
		From:    AppVersion(),
		At:      nowRFC3339(),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, blob, 0o600)
}

// takeInstallAttempt 读出记录并**随即删掉**。
//
// 删掉是语义的一部分：这句话只该说一遍。留着的话，一个装不上的版本会在此后每次
// 启动都弹同一句警告，而用户对此无能为力——那不是提示，是噪音。
func takeInstallAttempt() (installAttempt, bool) {
	path, err := attemptFile()
	if err != nil {
		return installAttempt{}, false
	}
	blob, err := os.ReadFile(path) //nolint:gosec // 路径由本应用的缓存目录拼出
	_ = os.Remove(path)
	if err != nil {
		return installAttempt{}, false
	}
	var att installAttempt
	if err := json.Unmarshal(blob, &att); err != nil {
		return installAttempt{}, false
	}
	if strings.TrimSpace(att.Version) == "" {
		return installAttempt{}, false
	}
	return att, true
}

// installAttemptNote 把"上次没装上"翻成给用户看的一句话；装成了、或者压根没试过
// 就返回空串。
//
// 判据是 **当前版本 < 记录里的目标版本**，不是"不相等"：用户完全可能在这中间手工
// 装了个更新的版本，那时上一次自动更新没走完也不再是个问题，不该再提。
func installAttemptNote() string {
	att, ok := takeInstallAttempt()
	if !ok {
		return ""
	}
	if compareVersions(AppVersion(), att.Version) >= 0 {
		return ""
	}
	when := ""
	if t, err := time.Parse(time.RFC3339, att.At); err == nil {
		when = t.Format("01-02 15:04") + " "
	}
	return fmt.Sprintf("%s那次更新到 %s 没有完成，当前仍是 %s。可以再试一次；若反复失败，请检查安全软件是否拦截了安装程序。",
		when, att.Version, AppVersion())
}
