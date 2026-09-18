package desktop

// install_runtime 的外壳侧（工具本身见 internal/runtimetool）：模型经它调用的，就是
// 设置页「运行时环境」那一个安装器（runtimes.go）——同一份清单、同一道 sha256、同一个
// 用户目录、同一次麒麟加白。模型能决定的只有"装三个里的哪一个"。
//
// 这里另有两件工具包做不了的事：
//
//   - **确保就绪**，而不只是"下载安装"：清单还没取过就先取；设置页那边正在装就等它装完；
//     装好了但安全中心还没放行（用户上次关掉了密码框）就补一次授权。模型不该为这几种
//     状态各学一种调用法。
//   - **给审批弹窗配一句人话**（runtimeResolver）：引擎的审批请求里不带工具参数，弹窗
//     只看得到一个裸工具名。用户要批准的是"下载 46 MB 的 Python 装进我的用户目录"，
//     这件事必须在按下「允许」之前就看得见。

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"

	"github.com/wt68/runcode/internal/protocol"
	"github.com/wt68/runcode/internal/runtimetool"
)

// runtimeBusyPoll 是"那边正在装"时回头看一眼的间隔。
const runtimeBusyPoll = 300 * time.Millisecond

// runtimeInstaller 把 App 适配成 runtimetool.Installer。
//
// 单独一个类型而不是在 App 上挂一个导出的 EnsureRuntime：App 的导出方法会被 Wails
// 绑给前端，而这个入口只该给模型用。
type runtimeInstaller struct{ app *App }

// EnsureRuntime 见 runtimetool.Installer。
func (r runtimeInstaller) EnsureRuntime(ctx context.Context, id string) (string, error) {
	return r.app.ensureRuntime(ctx, id)
}

// ensureRuntime 让一个运行时变得可用，返回给模型的那段话（英文，模型读的）。
//
// 报错也是写给模型的：每一条都说清楚接下来该怎么做，尤其是"别换别的法子装"——模型
// 在工具失败之后最常见的反应就是自己动手（见 runtimeenv.go 的 missingLine）。
func (a *App) ensureRuntime(ctx context.Context, id string) (string, error) {
	m := a.rt
	spec, ok := specFor(id)
	if !ok || m == nil {
		return "", fmt.Errorf("unknown runtime %q", id)
	}
	if !packPublished(id, runtime.GOOS) {
		// Linux/macOS 的 Git：本来就不发包（见 packPublished），指到设置页是死胡同。
		return "", fmt.Errorf("this app does not ship %s for this platform. If the task really needs it, ask the user"+
			" whether to install it with the system package manager (for example `sudo apt install git`)", spec.label)
	}
	// 设置页那边正在装（或正在授权）：等它做完再看结果，别叫模型"稍后重试"——它没有
	// "稍后"，只会立刻再调一次，或者自己去装。
	if err := m.waitIdle(ctx, id); err != nil {
		return "", runtimeCancelled(spec)
	}

	if m.isReady(id) {
		if m.pendingAuthorization(id) {
			// 上次的密码框被关掉了，或者之后 pip 装了新的 C 扩展（见 recheckTrust）。
			// 失败不算这次调用失败：运行时是好的，结果里会如实说"还没放行"。
			if err := a.authorizeRuntime(ctx, id); err != nil {
				debugLog("install_runtime: authorize %s: %v", id, err)
			}
		}
		return m.readyReport(id), nil
	}

	if !m.hasPackage(id) {
		// 清单还没取过（启动后那趟自动检查延后了几秒，或者当时断网）：现取一次。
		fctx, cancel := context.WithTimeout(ctx, runtimeFetchTimeout)
		info := a.refreshRuntimes(fctx)
		cancel()
		if !m.hasPackage(id) {
			if info.Error != "" {
				return "", fmt.Errorf("could not fetch the app's runtime list (%s). Tell the user; once the network is back they can"+
					" retry, or install it from 设置 → 运行时环境. Do not install %s any other way", info.Error, spec.label)
			}
			return "", fmt.Errorf("no %s package is published for this platform (%s) yet. Tell the user; do not install it any other way",
				spec.label, runtimePlatform())
		}
	}

	if err := a.installRuntime(ctx, id); err != nil {
		if errors.Is(err, context.Canceled) {
			return "", runtimeCancelled(spec)
		}
		return "", fmt.Errorf("installing %s failed: %v. Tell the user what failed; they can retry from 设置 → 运行时环境."+
			" Do not install it any other way", spec.label, err)
	}
	return m.readyReport(id), nil
}

// runtimeCancelled 是"用户停了"的那句话：停止回合、或在设置页按了取消。
func runtimeCancelled(spec packSpec) error {
	return fmt.Errorf("installing %s was cancelled by the user. Do not retry unless they ask for it", spec.label)
}

// ---- 状态查询（runtimeManager 上的小工具，只给本文件用） ------------------------

// waitIdle 等到这个包手上没有正在跑的安装/授权。
func (m *runtimeManager) waitIdle(ctx context.Context, id string) error {
	t := time.NewTicker(runtimeBusyPoll)
	defer t.Stop()
	for {
		m.mu.Lock()
		st := m.packs[id]
		busy := st != nil && st.cancel != nil
		m.mu.Unlock()
		if !busy {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (m *runtimeManager) isReady(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.packs[id]
	return st != nil && st.stage == protocol.RuntimeStageReady && st.dir != ""
}

func (m *runtimeManager) pendingAuthorization(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.packs[id]
	return st != nil && st.needsAuthorize
}

func (m *runtimeManager) hasPackage(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.packs[id]
	return st != nil && strings.TrimSpace(st.avail.URL) != ""
}

// readyReport 是装好之后回给模型的那段话。
//
// 要点有三：**完整路径**（PATH 之外的兜底——哪怕某个子进程拿的是旧环境，写全路径
// 总能跑）；**"这个对话里就能用"**，并明说系统提示词里那句"没有"已经过时——提示词是
// 开对话时拍的快照，不说的话模型会在两条互相矛盾的信息之间犹豫；**还没放行时如实说**，
// 否则它看到 126 会去改脚本。
func (m *runtimeManager) readyReport(id string) string {
	m.mu.Lock()
	st := m.packs[id]
	spec, version, dir, notes, pending := st.spec, st.version, st.dir, st.avail.Notes, st.needsAuthorize
	m.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s is installed (the app's own copy, in %s) and is on PATH for Bash from now on, in this conversation too:"+
		" run it as `%s`.", spec.label, version, dir, promptCommand(id))
	if bins := spec.binDirs(runtime.GOOS); len(bins) > 0 {
		fmt.Fprintf(&b, " Its programs are in %s.", filepath.Join(dir, bins[0]))
	}
	if id == protocol.RuntimePackPython {
		if libs := libsFromNotes(notes, spec.label, version); libs != "" {
			fmt.Fprintf(&b, " Preinstalled libraries: %s.", libs)
		}
	}
	b.WriteString(" The runtime section of your instructions was written before this install; where it says otherwise, this is current.")
	if pending {
		fmt.Fprintf(&b, " However, the system security center has not trusted it yet (the password dialog was cancelled or failed):"+
			" every run pops up a confirmation on the user's desktop and fails with exit code 126 if nobody accepts within 30 seconds."+
			" Tell the user; they can click 授权 next to it in 设置 → 运行时环境, or you can call %s again once they are ready to enter"+
			" their password.", runtimetool.Name)
	}
	return b.String()
}

// installSummary 是审批弹窗里那一句"要批准的是什么"（中文，用户读的）；不认识的 id 给 ""。
func (m *runtimeManager) installSummary(id string) string {
	spec, ok := specFor(id)
	if !ok || m == nil {
		return ""
	}
	m.mu.Lock()
	st := m.packs[id]
	stage, version, pending := st.stage, st.version, st.needsAuthorize
	avail, size := st.avail.Version, st.avail.Size
	m.mu.Unlock()

	kysec := kysecExecControlOn()
	if stage == protocol.RuntimeStageReady {
		if pending && kysec {
			return fmt.Sprintf("请麒麟安全中心放行已装好的 %s %s：接下来会请你在应用的密码框里输一次系统密码。", spec.label, version)
		}
		return fmt.Sprintf("%s %s 已经装好，这次只是确认它可用，不会下载任何东西。", spec.label, version)
	}
	name := spec.label
	if avail != "" {
		name += " " + avail
	}
	if size > 0 {
		name += fmt.Sprintf("（约 %d MB）", (size+(1<<20)-1)>>20)
	}
	s := "安装应用自带的 " + name + "：从应用的下载源获取，校验 sha256 后解压到你的用户目录，不需要管理员权限；装好后当前对话就能直接用。"
	if kysec {
		s += "装完会请你输一次系统密码，让麒麟安全中心放行。"
	}
	return s
}

// runtimeResolver 给 install_runtime 的审批请求配上 installSummary 那句话。
//
// 放在 Action 的 command_summary 元数据里：它经 ApprovalSummary 原样送到前端，而那是
// 审批请求里唯一一个"宿主能填、前端能读"的自由文本位。它是纯展示——策略、授权键、
// 裁判地板都不看它（授权键看的是工具参数本身，见引擎 HostToolSessionKey）。
type runtimeResolver struct {
	inner permissions.Resolver
	rt    *runtimeManager
}

// Resolve 见 permissions.Resolver。
func (r runtimeResolver) Resolve(ctx context.Context, req permissions.ResolveRequest) (permissions.Action, error) {
	action, err := r.inner.Resolve(ctx, req)
	if err != nil || req.ToolName != runtimetool.Name || r.rt == nil {
		return action, err
	}
	desc := r.rt.installSummary(runtimetool.RuntimeID(req.Input))
	if desc == "" {
		return action, nil
	}
	// 复制一份再改：Metadata 是 inner 返回的 map，可能被它缓存或与别处共享。
	md := make(map[string]any, len(action.Metadata)+1)
	for k, v := range action.Metadata {
		md[k] = v
	}
	md[permissions.MetadataCommandSummary] = desc
	action.Metadata = md
	return action, nil
}
