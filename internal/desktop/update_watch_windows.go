//go:build windows

package desktop

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	// watchAppExitTimeoutMS 是等发起更新的应用退干净的上限（2 分钟）。它退不掉的话
	// 安装器那边也装不了（NSIS 会因为文件被占用而放弃，见 project.nsi 的
	// waitForAppExit），等更久没有意义。
	//
	// 直接写成毫秒的 uint32，不写成 time.Duration：WaitForSingleObject 的参数单位
	// 本来就是毫秒的 uint32，转一道只会凭空多出一次可能溢出的类型转换。
	watchAppExitTimeoutMS uint32 = 2 * 60 * 1000
	// watchInstallTimeout 是等安装完成的上限。装一个几十 MB 的包用不了这么久；
	// 它留的余量是**用户对着 UAC 发呆**的那段时间。超时不是灾难：看门进程仍会把
	// 旧版本拉起来，而"没装上"这件事由 installAttempt 如实报出来。
	watchInstallTimeout = 10 * time.Minute
	// watchPollInterval 是轮询间隔。读一次注册表的开销可以忽略，取小一点让"应用
	// 消失"到"应用回来"之间的空窗尽量短——那段空白是用户最容易以为它崩了的时刻。
	watchPollInterval = 500 * time.Millisecond
	// watchSettleDelay：注册表写好之后安装器还要建快捷方式、写卸载器，等一下再
	// 拉起应用，别跟收尾动作撞上。
	watchSettleDelay = 2 * time.Second
)

// RunUpdateWatch 是看门模式的整个生命周期，返回进程退出码。
//
// 三步：等发起更新的应用退干净 → 等新版本真的落地 → 把应用拉起来。
//
// **不管装没装成，最后都会把应用拉起来**，这是有意的。安装失败时安装目录里躺着的
// 仍是完好的旧版本，把它打开是对的；而"关掉之后再也不回来"是这条链路最不能接受的
// 结局——用户此刻正对着一个消失了的应用，他没有任何线索。装没装成由应用自己在下次
// 启动时判定并报出来（见 installAttempt），比在这里猜准得多。
func RunUpdateWatch(args []string) int {
	w, err := parseWatchArgs(args)
	if err != nil {
		debugLog("update watch: 参数不对: %v", err)
		return 2
	}
	debugLog("update watch: 开始（pid=%d 目标=%s exe=%s）", w.PID, w.Expect, w.Exe)

	waitProcessExit(w.PID, watchAppExitTimeoutMS)

	if waitInstalled(w.Exe, w.Expect, watchInstallTimeout) {
		debugLog("update watch: %s 已装好", w.Expect)
		if w.Installer != "" {
			if err := os.Remove(w.Installer); err != nil && !os.IsNotExist(err) {
				debugLog("update watch: 删安装包失败（无碍）: %v", err)
			}
		}
	} else {
		debugLog("update watch: 等不到 %s 装好，仍把现有版本拉起来", w.Expect)
	}

	if err := relaunchApp(w.Exe); err != nil {
		debugLog("update watch: 拉起应用失败: %v", err)
		return 1
	}
	return 0
}

// waitProcessExit 等 pid 那个进程退出。
//
// 拿进程句柄等，不是轮询进程列表：句柄在 OpenProcess 成功那一刻就绑死了这一个
// 进程实例，不会被 PID 复用骗到。打不开（已经退了、或者没权限）就直接往下走——
// 后面还有"新版本装好了没有"那一层判据兜底。
func waitProcessExit(pid int, timeoutMS uint32) {
	// 上下界都要判：Windows 的 PID 是 32 位无符号数，而 pid 是从命令行解析出来的
	// int——不判的话，一个写坏的参数会被截断成另一个**真实存在**的进程号，于是这里
	// 变成"等一个毫不相干的进程退出"。
	if pid <= 0 || uint64(pid) > math.MaxUint32 {
		return
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid)) //nolint:gosec // G115：上一行刚判过 0 < pid <= MaxUint32
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(h) }()
	_, _ = windows.WaitForSingleObject(h, timeoutMS)
}

// waitInstalled 等到「装好了」为止，返回是否等到。
//
// 判据是两条同时成立：
//
//  1. 「添加或删除程序」里这个安装目录对应的那一项，DisplayVersion 已经不低于要装的
//     版本。NSIS 写注册表（wails.writeUninstaller）排在复制文件之后、快接近末尾，
//     所以它变新意味着文件已经铺完；
//  2. 应用 exe 能以**独占**方式打开，即没有任何人还占着它。
//
// 为什么不去读 exe 自己的版本号（那才是真正的事实）：本应用的 exe 没有链进版本资源
// （wails3 generate syso 的产物目前没有参与链接），读不出来；而"跑一下它问它是几"
// 意味着在更新过程中执行安装目录里的程序，代价是可能把一个 GUI 实例拉起来、抢走
// 单实例锁、反过来卡住安装。用注册表这个只读信号更安全。
//
// 注册表**确实骗过一次人**——静默模式下 NSIS 遇到写不动的文件会跳过并继续，于是
// 留下"注册表 0.1.3、exe 还是 0.1.2"。那条路现在被 project.nsi 的 waitForAppExit
// 堵上了（占用就整个放弃，不写任何东西）；万一还是漏过去，最终判定也不靠这里：
// 应用下次启动会拿自己的真实版本跟 installAttempt 一比，如实报出来。
func waitInstalled(exe, want string, timeout time.Duration) bool {
	root := filepath.Dir(exe)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(watchPollInterval)
		got, ok := installedVersionAt(root)
		if !ok || compareVersions(got, want) < 0 {
			continue
		}
		if !fileUnlocked(exe) {
			continue
		}
		time.Sleep(watchSettleDelay)
		return true
	}
	return false
}

// fileUnlocked 报告 path 此刻没有被任何进程占用。
//
// 用 CreateFile 只要读权限、但**不共享**（dwShareMode = 0）：别人开着它就失败。
// 不能用 os.OpenFile(O_RDWR) 来试——安装目录在 Program Files 下，本进程是普通权限，
// 那样只会得到一个永远为假的"被占用"。
func fileUnlocked(path string) bool {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(h)
	return true
}

// uninstallRoot 是「添加或删除程序」那张表在注册表里的位置。
const uninstallRoot = `Software\Microsoft\Windows\CurrentVersion\Uninstall`

// installedVersionAt 找出装在 root 的那个应用，返回它登记的 DisplayVersion。
//
// **按安装目录反查，不按键名直取**。键名是 NSIS 编译期的一个 define
// （UNINST_KEY_NAME，默认「公司名+产品名」），品牌换一次、产品名改一个字它就变了，
// 而在 Go 这边硬写一份就是给这个功能埋第二处"改了名字之后静默失效"的地方——这一轮
// 已经吃过四次这类亏了。按目录反查跟品牌无关，天然对得上。
//
// HKLM 与 HKCU 都要查：machine 范围写 HKLM、user 范围（--install-scope user）写
// HKCU，而应用这边并不知道用户当初装的是哪一种。
func installedVersionAt(root string) (string, bool) {
	root = normalizeDir(root)
	if root == "" {
		return "", false
	}
	for _, hive := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		if v, ok := scanUninstall(hive, root); ok {
			return v, true
		}
	}
	return "", false
}

func scanUninstall(hive registry.Key, root string) (string, bool) {
	k, err := registry.OpenKey(hive, uninstallRoot, registry.ENUMERATE_SUB_KEYS|registry.WOW64_64KEY)
	if err != nil {
		return "", false
	}
	defer func() { _ = k.Close() }()
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return "", false
	}
	for _, name := range names {
		if v, ok := uninstallEntryVersion(k, name, root); ok {
			return v, true
		}
	}
	return "", false
}

// uninstallEntryVersion 看一条卸载项是不是装在 root，是就返回它的 DisplayVersion。
//
// 两级判定，和 project.nsi 里 reuseExistingInstallDir 的两级是同一套、方向相反：
// 先看 InstallLocation（本安装器自己写的，0.1.3 之后才有），退一步看 UninstallString
// 的父目录（更早的版本也有）。有第二级，从老版本升上来的机器第一次就认得出。
func uninstallEntryVersion(parent registry.Key, name, root string) (string, bool) {
	sub, err := registry.OpenKey(parent, name, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", false
	}
	defer func() { _ = sub.Close() }()

	loc, _, _ := sub.GetStringValue("InstallLocation")
	if normalizeDir(loc) != root {
		us, _, _ := sub.GetStringValue("UninstallString")
		us = strings.Trim(strings.TrimSpace(us), `"`)
		if us == "" || normalizeDir(filepath.Dir(us)) != root {
			return "", false
		}
	}
	ver, _, err := sub.GetStringValue("DisplayVersion")
	if err != nil {
		return "", false
	}
	ver = strings.TrimSpace(ver)
	return ver, ver != ""
}

// normalizeDir 把目录路径收敛成可比较的形式：去引号、去尾部分隔符、Clean、转小写。
// Windows 路径不区分大小写，而注册表里存的写法和 os.Executable() 给的写法经常在
// 大小写和尾部反斜杠上不一样。
func normalizeDir(p string) string {
	p = strings.Trim(strings.TrimSpace(p), `"`)
	if p == "" {
		return ""
	}
	p = strings.TrimRight(p, `\/`)
	if p == "" {
		return ""
	}
	return strings.ToLower(filepath.Clean(p))
}

// relaunchApp 把应用重新拉起来。
//
// 普通权限：看门进程是**应用**派生的，继承的是当前用户的中完整性级别，从它启动的
// 新版本也就是普通权限。这正是不让安装器（提权运行）自己去拉应用的原因——那样应用
// 会带着管理员身份跑，它写进数据目录的文件会变成 admin 属主，之后正常权限的那次
// 运行反而读不动自己上一次留下的会话和配置。
func relaunchApp(exe string) error {
	if _, err := os.Stat(exe); err != nil {
		return err
	}
	cmd := exec.Command(exe) //nolint:gosec // exe 由应用侧从自己的安装目录算出后传入
	cmd.Dir = filepath.Dir(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	if err := cmd.Start(); err != nil {
		return err
	}
	// 故意不 Wait：拉起来就走人，这个进程接着就要退出了。
	return cmd.Process.Release()
}
