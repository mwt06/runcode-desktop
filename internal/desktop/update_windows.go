//go:build windows

package desktop

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// canLaunchInstaller: Windows 的分发形态是 NSIS 安装包，可以由应用直接拉起。
func canLaunchInstaller() bool { return true }

// willAutoRestart 报告这次安装会不会走「静默 + 自动重启」那条路。判据与
// launchInstaller 完全一致：认得出自己装在哪，才敢静默装、也才知道装完拉起哪个 exe。
func willAutoRestart() bool { return nsisInstallDirArg(appInstallRoot()) != "" }

// launchInstaller 拉起安装器。expect 是这次要装成的版本（来自服务端清单），交给
// 看门进程当"装好了没有"的判据。
//
// 两条路，取决于**能不能认出自己装在哪**：
//
//   - 认得出（同目录有 uninstall.exe，即一次正经安装）：静默安装（/S）+ 指定目录
//     （/D=），并在退出前留下一个看门进程，等新版本落盘后把应用重新拉起来。用户
//     看到的只有一次 UAC，然后应用自己关掉、自己回来。既然他刚在设置页点了「立即
//     安装并重启」，装到哪儿、要不要装都已经表过态了，再走一遍四页向导是多余的。
//   - 认不出（开发构建跑在 bin/ 里）：退回**可见的向导**。这时既不知道会装到哪，
//     也就没法在装完之后把正确的那个 exe 拉起来；静默装一个看不见的东西，比多点
//     几下糟得多。
//
// 这里**必须**走 ShellExecute，不能用 os/exec：NSIS 安装包的清单要求管理员权限
// （project.nsi 的 REQUEST_EXECUTION_LEVEL 默认 admin），而 os/exec 底下是
// CreateProcess——它遇到这种 exe 会直接以 ERROR_ELEVATION_REQUIRED(740) 失败，
// **不会**弹 UAC。只有 ShellExecute 这条路会把提权对话框弹出来。
//
// 不指定动词（第二个参数传 nil）：要不要提权由安装包自己的清单说了算。硬写 "runas"
// 会把 --install-scope user 那种本来免 UAC 的安装包也强行拽进提权对话框。
//
// UAC 由**应用**在退出前弹，不挪进看门进程：用户刚点完按钮、窗口还在，提权框此刻
// 出现是有来由的；等应用都关没了再凭空冒出一个提权框，只会让人以为中招了。
func launchInstaller(path, expect string) error {
	root := appInstallRoot()
	dir := nsisInstallDirArg(root)
	if dir == "" {
		return shellExecute(path, "")
	}
	// 看门进程要在安装器动手**之前**起来：它开工第一件事是等发起更新的这个进程退干净。
	// 起不来不算致命——更新照做，只是装完要用户自己点图标。
	if self, err := os.Executable(); err != nil {
		debugLog("update: 定位自身失败，跳过自动重启: %v", err)
	} else if err := spawnUpdateWatcher(watchArgs{
		PID:       os.Getpid(),
		Exe:       self,
		Expect:    expect,
		Installer: path,
	}); err != nil {
		debugLog("update: 自动重启看门进程起不来（装完需手动打开）: %v", err)
	}
	return shellExecute(path, installerParams(dir))
}

// installerParams 拼安装器的命令行。dirArg 为空表示"不知道装在哪"，那时不静默、
// 也不指定目录，退回可见向导（见 launchInstaller）。
//
// /D= **必须排在最后**，这是 NSIS 的硬规则：它把 /D= 之后到行尾的内容整个当成路径。
// 所以 /S 只能在它前面，而且以后要再加参数也只能往前加。
func installerParams(dirArg string) string {
	if dirArg == "" {
		return ""
	}
	return "/S " + dirArg
}

// shellExecute 拉起 file，params 为空时不带参数。
func shellExecute(file, params string) error {
	exe, err := windows.UTF16PtrFromString(file)
	if err != nil {
		return err
	}
	var args *uint16
	if strings.TrimSpace(params) != "" {
		if args, err = windows.UTF16PtrFromString(params); err != nil {
			return err
		}
	}
	return windows.ShellExecute(0, nil, exe, args, nil, windows.SW_SHOWNORMAL)
}

// nsisInstallDirArg 返回传给安装器的 /D= 参数，root 不像一次 NSIS 安装时返回空串。
//
// 为什么要传：更新的语义是**「更新你正在跑的这一份」**，不是「更新注册表记得的那一
// 份」。两者在机器上装过不止一份、或者用户把目录挪过位置时会不一样，而那时用户的
// 预期毫无疑问是前者——他刚在这个窗口里点的更新。安装器那边也有一层按注册表沿用旧
// 目录的兜底（见 build/windows/nsis/project.nsi 的 reuseExistingInstallDir），管的是
// 用户手工双击安装包的场景；这里管的是应用自己拉起的场景，两层各有各的来路。
//
// 为什么要判 uninstall.exe：开发构建跑在 bin/ 里，那不是一次安装。不判的话，在开发
// 机上点一下「更新」会把正式版装进源码树的构建目录——而且是静悄悄地装进去。它同时
// 也是「敢不敢静默安装 + 自动重启」的判据，见 launchInstaller。
//
// NSIS 的两条硬规则：/D= 必须是**最后**一个参数，而且**不能加引号**，哪怕路径里有
// 空格（"C:\Program Files\国家开放大学\智开" 正是这种）。所以这里直接拼，不加引号，
// 也不在它后面再接别的参数。
func nsisInstallDirArg(root string) string {
	root = strings.TrimRight(strings.TrimSpace(root), `\/`)
	if root == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(root, "uninstall.exe")); err != nil {
		return ""
	}
	return "/D=" + root
}

// ---- 看门进程的派生 ------------------------------------------------------------

// spawnUpdateWatcher 起一个后台进程，跑本程序的看门模式（见 update_watch.go 的
// 整段说明：为什么要有它、为什么是本程序自己、为什么要跑副本）。
//
// DETACHED_PROCESS：它必须活过本应用的退出，那正是它存在的理由。
// CREATE_NO_WINDOW + HideWindow：本应用是 -H windowsgui 构建，平时没有控制台，
// 不藏的话会闪一个黑框。
func spawnUpdateWatcher(w watchArgs) error {
	bin, err := stageWatcher()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, buildWatchArgs(w)...) //nolint:gosec // bin 是本程序自己的副本，参数由本包拼装
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.DETACHED_PROCESS,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// 故意不 Wait：这个进程要活过本应用的退出。
	return cmd.Process.Release()
}

// stageWatcher 把本程序复制到更新缓存目录，返回副本路径。
//
// 为什么必须是副本：运行中的 exe 会被系统锁住，而安装器要覆盖的就是它——直接跑
// 安装目录里的自己，等于亲手把这次更新卡死。缓存目录在 %LOCALAPPDATA% 下，安装器
// 碰不到。
//
// 名字带版本号，加上"大小对得上就不重复拷"：一次更新里用户可能点好几次安装，没必要
// 每次都搬几十 MB。旧版本留下的副本由 cleanStaleWatchers 在下次启动时清掉——那时
// 它已经不再运行，删得掉。
func stageWatcher() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir, err := updateCacheDir()
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, watcherName())
	if di, err := os.Stat(dst); err == nil {
		if si, err := os.Stat(self); err == nil && si.Size() == di.Size() {
			return dst, nil
		}
	}
	if err := copyFileStreaming(self, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// watcherName 是副本的文件名。带版本号，好让"这一版的看门程序"和"上一版留下的"
// 是两个文件——升级之后旧的那个才删得掉（见 cleanStaleWatchers）。
func watcherName() string {
	return "updater-" + safeVersion(AppVersion()) + ".exe"
}

// cleanStaleWatchers 删掉不属于当前版本的看门程序副本，由启动时调用。
//
// 更新成功之后本应用的版本已经变了，上一版留下的那份副本（几十 MB）就成了纯粹的
// 垃圾。删不掉不报错：它可能还在跑（比如更新失败后它还在等），下次启动再删就是了。
func cleanStaleWatchers() {
	dir, err := updateCacheDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	keep := watcherName()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == keep || !strings.HasPrefix(name, "updater-") || !strings.HasSuffix(name, ".exe") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			debugLog("update: 清理旧看门程序 %s 失败（无碍）: %v", name, err)
		}
	}
}
