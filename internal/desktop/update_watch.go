package desktop

import (
	"errors"
	"flag"
	"io"
	"strconv"
	"strings"
)

// 更新看门进程的调用约定（实现在 update_watch_windows.go）。
//
// # 为什么要有一个"看门进程"
//
// 安装器要覆盖的正是**正在运行的那个 exe**，Windows 不允许改写运行中的可执行文件，
// 所以应用必须先退出。退出之后就没人能把新版本拉起来了——除非提前留下一个不会跟着
// 一起走的进程。
//
// # 为什么是本程序自己，而不是单独一个 updater.exe
//
// 这活儿以前是一段 PowerShell 脚本（`powershell -Command <十几行>`）。它有三个毛病，
// 每一个都表现为"应用关掉之后再也没回来"这一种无声的失败：
//
//  1. `powershell -Command <一大段脚本>` 是杀软和 EDR 的教科书级特征，会被直接掐掉；
//  2. PowerShell 不一定在（LTSC/Server Core 精简掉、组策略开了受限语言模式）；
//  3. 拼字符串拼出的语法错误在 Go 这边一个字都不报，只让那个没人会看的后台进程
//     起来就死。
//
// 换成一个真正的程序即可根治。而"单独打一个 updater.exe"没必要：多一个构建目标、
// 多一处将来要签名的东西、还多一种更新器与应用版本错位的可能。用应用自己的二进制
// 加一个隐藏模式，这三样全省了——品牌注入、路径解析、诊断日志也都是现成的。
//
// # 为什么要跑一份副本
//
// 运行中的 exe 会锁住自己的文件，而安装器要覆盖的就是它。所以看门进程跑的是复制到
// **安装目录之外**（更新缓存目录）的那一份，见 stageWatcher。
//
// # 这个模式必须在一切之前处理掉
//
// 见 IsUpdateWatch。

// updateWatchFlag 是进入看门模式的开关，必须是第一个参数。
const updateWatchFlag = "--update-watch"

// IsUpdateWatch 报告这次启动是不是看门模式。
//
// 调用点必须在 **Wails 起来之前、单实例锁之前**（见 cmd/runcode-desktop/main.go）。
// 晚一步的后果不是"多一个后台进程"：单实例锁按品牌全局唯一，看门进程一旦去抢，
// 轻则它自己发现锁被占后静默退出（于是没人拉起新版本），重则它抢在前面，把用户
// 真正的那次启动挡在门外——而那一次的表现是"双击图标什么都没发生"。
func IsUpdateWatch(args []string) bool {
	return len(args) > 1 && args[1] == updateWatchFlag
}

// watchArgs 是交给看门进程的全部上下文。它们都由应用侧算好后传进来，看门进程
// 自己不做任何推断——它跑在一份复制出来的二进制里，`os.Executable()` 指向的是那份
// 副本，安装目录、品牌、版本一概推不出来。
type watchArgs struct {
	// PID 是发起更新的那个应用进程。看门进程先等它退干净，再去看装好了没有——
	// 否则会对着旧 exe 白等一轮。
	PID int
	// Exe 是安装完成后要拉起的可执行文件（安装目录里的那个）。
	Exe string
	// Expect 是这次要装成的版本，取自服务端清单。
	Expect string
	// Installer 是安装包路径，装成了就删掉——它上百 MB，留着没有意义。
	Installer string
}

// buildWatchArgs / parseWatchArgs 是同一套约定的两端，成对改。
//
// 用 argv 而不是拼一行命令：Windows 上 Go 的 exec 会按 CommandLineToArgvW 的规则
// 转义每个参数，子进程再原样解析回来。中文、空格、单引号、反斜杠结尾的目录全都
// 原样送达——这是那段 PowerShell 时代真正踩过的一类坑。
func buildWatchArgs(w watchArgs) []string {
	return []string{
		updateWatchFlag,
		"--pid", strconv.Itoa(w.PID),
		"--exe", w.Exe,
		"--expect", w.Expect,
		"--installer", w.Installer,
	}
}

// parseWatchArgs 解析 os.Args。args[0] 是程序名、args[1] 是 updateWatchFlag，
// 真正的参数从 args[2] 开始。
func parseWatchArgs(args []string) (watchArgs, error) {
	if !IsUpdateWatch(args) {
		return watchArgs{}, errors.New("不是看门模式的参数")
	}
	var w watchArgs
	fs := flag.NewFlagSet("update-watch", flag.ContinueOnError)
	// 这个模式没有界面也没有控制台，用法说明写给谁看都没有，出错交给调用方记日志。
	fs.SetOutput(io.Discard)
	fs.IntVar(&w.PID, "pid", 0, "")
	fs.StringVar(&w.Exe, "exe", "", "")
	fs.StringVar(&w.Expect, "expect", "", "")
	fs.StringVar(&w.Installer, "installer", "", "")
	if err := fs.Parse(args[2:]); err != nil {
		return watchArgs{}, err
	}
	w.Exe = strings.TrimSpace(w.Exe)
	w.Expect = strings.TrimSpace(w.Expect)
	w.Installer = strings.TrimSpace(w.Installer)
	if w.Exe == "" {
		return watchArgs{}, errors.New("缺少 --exe")
	}
	if w.Expect == "" {
		return watchArgs{}, errors.New("缺少 --expect")
	}
	return w, nil
}
