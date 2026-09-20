package desktop

// pip 装完新库，立刻请人放行（麒麟专属）。
//
// # 为什么要有这一层
//
// 装运行时包时已经把包里全部 ELF 加白一次（见 kysec.go）。但模型干活时会自己 `pip
// install`，新库里的 C 扩展（.so）对安全中心仍是 unknown——一个已放行的 Python 去加载
// 它照样被拒。启动时的 recheckTrust 能发现这件事，可那要等到**下次开应用**；在那之前
// 用户看到的是脚本莫名其妙地失败，或者设置页角落里多了一个「授权」按钮。
//
// 所以在这里补两件事：
//
//  1. **装完就问**：每条 Bash 命令跑完，看一眼运行时目录动没动；动了就扫一遍，有新的
//     未放行文件立刻弹密码框，并在框里说清楚是哪个库带来的。
//  2. **不抢跑**：模型的下一条 Bash 命令在权限检查那一步先等这次授权做完（见
//     runtimetool.go 的 runtimeResolver）。否则用户还在输密码，模型已经去 import 新库，
//     撞上一句 "failed to map segment" 然后开始改脚本——而问题根本不在脚本里。
//
// # 三条边界
//
//   - **检查必须便宜**：每条命令都会触发。先比对几个安装目录的修改时间（几次 stat），
//     变了才去走那趟六千文件的全量扫描。
//   - **拒绝要被记住**：用户关掉密码框之后，同一批文件不再自动弹第二次——否则接下来
//     每条命令都弹一次，比不做还糟。换了新文件（指纹变）或用户自己点「授权」才重来。
//   - **不提权就别问**：执行控制没开（非麒麟、或麒麟上关掉了）整条链路直接跳过。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/protocol"
)

// trustGate 是"同一时刻只做一次加白"的闸。容量 1 的 channel 而不是 sync.Mutex：
// 等待方要能被回合的取消叫醒（用户按了停止，不该还卡在那儿等密码框）。
type trustGate struct{ ch chan struct{} }

func newTrustGate() *trustGate { return &trustGate{ch: make(chan struct{}, 1)} }

// acquire 抢到闸返回 true；ctx 结束返回 false（没抢到就不要 release）。
func (g *trustGate) acquire(ctx context.Context) bool {
	select {
	case g.ch <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (g *trustGate) release() { <-g.ch }

// wait 等到没有加白在跑。模型的下一条命令经它排队。
func (g *trustGate) wait(ctx context.Context) {
	if g.acquire(ctx) {
		g.release()
	}
}

// autoTrustTimeout 给一次自动加白的总上限（含用户输密码的时间）。
//
// 比 askpassTimeout 略宽：那是"等人输密码"的上限，这里还要加上扫描与 kysec_set 的时间。
const autoTrustTimeout = askpassTimeout + 2*time.Minute

// bashToolName 是引擎里跑命令的那个工具。装库只可能经它发生（模型没有别的路子写
// 运行时目录），加白的两个钩子也都只认它。
const bashToolName = "Bash"

// noteToolFinished 是每条工具命令跑完后的钩子（hostSinkAdapter 在事件里调它）。
//
// 只认 Bash：别的工具动不了运行时目录。**不阻塞**——调用方是事件投递路径，卡住它
// 会把整条会话的事件流一起卡住。
func (a *App) noteToolFinished(toolName string) {
	if toolName != bashToolName || a == nil || a.rt == nil || !kysecExecControlOn() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), autoTrustTimeout)
		defer cancel()
		a.autoTrustRuntimes(ctx)
	}()
}

// autoTrustRuntimes 扫一遍已装的运行时，有新的未放行文件就请用户授权。
func (a *App) autoTrustRuntimes(ctx context.Context) {
	if !a.trust.acquire(ctx) {
		return
	}
	defer a.trust.release()
	for _, s := range packSpecs {
		if ctx.Err() != nil {
			return
		}
		a.autoTrustPack(ctx, s.id)
	}
}

// autoTrustPack 是单个包的那一趟：目录没动就走人，动了才扫，扫出新东西才弹框。
// 调用方持 trustGate。
func (a *App) autoTrustPack(ctx context.Context, id string) {
	m := a.rt
	m.mu.Lock()
	st := m.packs[id]
	if st == nil || st.stage != protocol.RuntimeStageReady || st.dir == "" || st.cancel != nil {
		m.mu.Unlock()
		return
	}
	dir, lastStamp, declined := st.dir, st.trustStamp, st.trustDeclined
	m.mu.Unlock()

	stamp := installStamp(id, dir)
	if stamp != "" && stamp == lastStamp {
		return // 安装目录一个字节都没动过
	}
	pending, files, err := needsTrust(dir)
	if err != nil {
		debugLog("auto trust %s: %v", id, err)
		return
	}
	fingerprint := elfFingerprint(dir, files)
	m.update(id, func(s *packState) {
		s.trustStamp = stamp
		s.needsAuthorize = pending
	})
	if !pending {
		return
	}
	if fingerprint == declined {
		// 用户刚关掉过这一批的密码框。设置页那个「授权」按钮仍然亮着，由他决定什么时候点。
		return
	}
	if err := trustNow(a, ctx, id, dir, autoTrustPurpose(m.label(id), dir, files)); err != nil {
		debugLog("auto trust %s: %v", id, err)
		m.update(id, func(s *packState) { s.trustDeclined = fingerprint })
		return
	}
	m.update(id, func(s *packState) {
		s.needsAuthorize = false
		s.trustDeclined = ""
		s.trustStamp = installStamp(id, dir)
	})
	m.publish()
}

// trustNow 是真正去弹密码框、调 kysec_set 的那一步。是个变量只为**可测**：这条链路
// 的其余部分（什么时候查、查出什么、拒绝了怎么记）都该能在没有图形会话的机器上跑。
var trustNow = func(a *App, ctx context.Context, id, dir, purpose string) error {
	return a.trustRuntime(ctx, id, dir, purpose)
}

// autoTrustPurpose 是密码框上那句话：**它是用户判断该不该输密码的唯一依据**，所以要
// 说出是谁带来的（哪个库）、不授权会怎样，而不是一句"需要权限"。
func autoTrustPurpose(label, dir string, files []elfFile) string {
	names := newlyAddedNames(dir, files)
	who := "刚装的新库"
	if len(names) > 0 {
		who = "刚装的 " + strings.Join(names, "、")
	}
	return fmt.Sprintf("%s里有新的程序文件要让麒麟安全中心放行（%s 运行时）。"+
		"不放行也能留着，只是用到它的脚本一跑就会被安全中心拦下。", who, label)
}

// newlyAddedNames 猜这批 ELF 里"新来的"属于哪些库：比加白标记新的文件，取它在
// site-packages / node_modules 下的那一层目录名。
//
// 只是为了把密码框上那句话说具体，猜不出来就不说——宁可笼统，不可编造。
func newlyAddedNames(dir string, files []elfFile) []string {
	info, err := os.Stat(filepath.Join(dir, kysecMarker))
	if err != nil {
		return nil
	}
	since := info.ModTime().UnixNano()
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		if f.mtime <= since {
			continue
		}
		name := libraryName(dir, f.path)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	if len(out) > 4 {
		out = append(out[:4:4], fmt.Sprintf("等 %d 个库", len(out)))
	}
	return out
}

// libraryName 从包内路径里取出库名：site-packages/<库>/… 或 node_modules/<库>/…。
func libraryName(dir, path string) string {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, p := range parts {
		if (p == "site-packages" || p == "node_modules") && i+1 < len(parts) {
			name := parts[i+1]
			// 顶层就是 .so 的库（markupsafe 那种单文件扩展）取不到目录名，去掉后缀凑合。
			if strings.Contains(name, ".") && i+2 >= len(parts) {
				name, _, _ = strings.Cut(name, ".")
			}
			return name
		}
	}
	return ""
}

// installStamp 是"安装目录有没有动过"的便宜判据：装/删/升级一个库都会改到这几个目录
// 自身的修改时间，而 stat 几次是微秒级的——全量扫描（六千个文件、上百毫秒）只在它
// 变了之后才做。
//
// 取不到（目录不存在、权限不对）返回 ""，调用方按"动过"处理：宁可多扫一次。
func installStamp(id, dir string) string {
	var b strings.Builder
	for _, d := range installWatchDirs(id, dir) {
		info, err := os.Stat(d)
		if err != nil {
			return ""
		}
		fmt.Fprintf(&b, "%s=%d;", filepath.Base(d), info.ModTime().UnixNano())
	}
	return b.String()
}

// installWatchDirs 是一个包里"装了新东西就会变"的目录。
//
// 故意只盯这两三个：site-packages / node_modules 下加一个库必然改到它们自身的修改
// 时间，而 bin 管住 pip 装的命令行工具。盯整棵树没有必要，那正是我们想避开的开销。
func installWatchDirs(id, dir string) []string {
	var out []string
	switch id {
	case protocol.RuntimePackPython:
		// 版本号在路径里（lib/python3.12/site-packages），用通配找，别写死版本。
		matches, _ := filepath.Glob(filepath.Join(dir, "lib", "python3.*", "site-packages"))
		out = append(out, matches...)
		out = append(out, filepath.Join(dir, "lib", "site-packages")) // Windows 布局
	case protocol.RuntimePackNode:
		out = append(out, filepath.Join(dir, "lib", "node_modules"))
	}
	out = append(out, filepath.Join(dir, "bin"))
	// 不存在的目录（平台布局不同）直接剔掉，否则 installStamp 会一直返回 ""、
	// 把每条命令都拖去做一次全量扫描。
	keep := out[:0]
	for _, d := range out {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			keep = append(keep, d)
		}
	}
	return keep
}
