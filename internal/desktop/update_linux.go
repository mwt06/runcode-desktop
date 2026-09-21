//go:build linux

package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Linux 上由应用自己装更新：应用的密码框授权 → sudo -A apt-get install 已校验的 deb
// → 拉起新版本 → 本进程退出。
//
// 为什么不交给系统的软件包安装器（双击 deb 那条路）：那样用户还得再点一次「安装」、
// 再输一次密码，装完还要自己重开应用——「点一下就装好」做不到。apt 与系统安装器
// 最终走的是同一个 dpkg，麒麟安全中心的来源检查照样生效，应用绕不过去，也不该绕。
//
// 授权沿用运行时包加白那一套（trustRuntime）：应用自己发起的提权，用 "app:" 开头的
// 键上膛、带一句给人看的用途，做完立刻撤膛并 sudo -k，不留窗口。

// aptInstallTimeout 是一次安装的上限：包括等用户在密码框里输密码（askpassTimeout）、
// 等别的 apt 放锁（aptLockWait）、以及 apt 顺带去源里拉 Recommends。
const aptInstallTimeout = 15 * time.Minute

// relaunchScript 是装完之后把新版本拉起来的那段 sh：等 $1（本进程）退出，再 exec $2。
//
// 为什么要等：单实例锁攥在本进程手里，新版本抢先起来会看见锁、把焦点交给旧窗口然后
// 自己退出——用户看到的是旧版本留在屏幕上。所以先等旧的退干净。
// 参数走位置参数而不是拼进脚本：路径里有空格或引号也不会变成另一条命令。
const relaunchScript = `while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; exec "$2"`

// updateInstallable 判断这台机器上能不能由应用接管安装。三条都满足才行：
//
//   - 有 DISPLAY：sudo 没有终端时改走 askpass 的前提（同 askpassReady）。
//   - 有 sudo 与 apt-get。
//   - 正在跑的这个二进制**归 dpkg 管**，而且包名就是本产品。开发构建、解压出来的
//     二进制都不满足——在那儿点「安装」会让 apt 把正式版装到 /opt/apps，而用户
//     正在跑的那一份原地不动，看起来像是更新了个寂寞。这时退回「打开安装包所在
//     文件夹」。
//
// 只算一次：答案在一次运行里不会变，而 dpkg-query 要起一个进程。
var updateInstallable = sync.OnceValue(func() bool {
	if os.Getenv("DISPLAY") == "" {
		return false
	}
	for _, bin := range []string{"sudo", "apt-get", "dpkg-query"} {
		if _, err := exec.LookPath(bin); err != nil {
			return false
		}
	}
	exe, err := selfExecutable()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "dpkg-query", "-S", exe).Output() //nolint:gosec // 查询本程序自身归属
	if err != nil {
		return false
	}
	pkg, _, _ := strings.Cut(strings.TrimSpace(string(out)), ":")
	return strings.TrimSpace(pkg) == AppProduct()
})

func canLaunchInstaller() bool { return updateInstallable() }

// willAutoRestart: 由应用接管安装时，装好后一定会把新版本拉起来（见 relaunchAfterExit）。
func willAutoRestart() bool { return canLaunchInstaller() }

// installHint：接管安装时说点了会发生什么；不接管时说打开文件夹之后怎么做——
// 麒麟的文件管理器里双击 deb 打开的是系统的软件包安装器（真机实测），装完要重开
// 应用，因为正在跑的这个进程还是旧的二进制。
func installHint() string {
	if canLaunchInstaller() {
		return "输入一次系统密码即可完成安装，装好后会自动重新打开"
	}
	return "双击其中的安装包，用系统的软件包安装器装好后，重新打开本应用"
}

// runInstaller 以 root 身份用 apt 装 file，成功后安排新版本在本进程退出后启动。
// 阻塞到 apt 结束（含用户输密码的时间），之后由 InstallUpdate 让本进程退出。
func (a *App) runInstaller(file, expect string) error {
	const key = "app:update"
	env := a.askpassEnv(key)
	if env == nil {
		return errors.New("本机弹不出授权框（没有图形会话），无法自动安装更新")
	}
	purpose := fmt.Sprintf("安装新版本 %s（已下载并校验过的安装包）。装好后应用会自动重新打开。", expect)
	// 上膛只覆盖这一次安装，做完立刻撤：应用自己发起的提权不留窗口。
	a.privGate.armFor(key, purpose, askpassTimeout+time.Minute)
	defer a.privGate.disarm(key)

	ctx, cancel := context.WithTimeout(context.Background(), aptInstallTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", aptInstallArgs(file)...) //nolint:gosec // 固定的 apt-get 调用，file 是刚重新校验过 sha256 的安装包
	cmd.Env = envWith(env)
	out, err := cmd.CombinedOutput()
	// 撤掉这次 sudo 留下的凭据缓存，理由同 kysecTrust。
	_ = exec.Command("sudo", "-k").Run() //nolint:gosec // 固定参数
	if err != nil {
		debugLog("update: apt 安装失败: %v\n%s", err, out)
		return aptInstallError(string(out), err)
	}
	debugLog("update: apt 安装完成\n%s", lastLines(string(out), 5))

	// 装已经成功了，拉不起新版本只是少了「自动回来」：记下来，不当失败报。
	if err := relaunchAfterExit(); err != nil {
		debugLog("update: 安排重新启动失败（需手动打开）: %v", err)
	}
	return nil
}

// relaunchAfterExit 起一个脱离本进程的 sh，等本进程退出后 exec 新装好的二进制。
//
// Setsid：它必须活过本应用的退出，也不该收到发给本应用进程组的信号。
// 路径用本进程自己的可执行文件：dpkg 已经把同一路径换成了新版本。
func relaunchAfterExit() error {
	exe, err := selfExecutable()
	if err != nil {
		return err
	}
	cmd := exec.Command("/bin/sh", "-c", relaunchScript, "relaunch", strconv.Itoa(os.Getpid()), exe) //nolint:gosec // 固定脚本，参数走位置参数
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// 故意不 Wait：这个进程要活过本应用的退出。
	return cmd.Process.Release()
}

// cleanStaleWatchers: Linux 上没有看门进程（重启由 relaunchAfterExit 的 sh 负责），
// 也就没有它的副本要清。
func cleanStaleWatchers() {}
