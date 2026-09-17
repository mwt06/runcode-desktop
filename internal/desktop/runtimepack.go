package desktop

// 运行时包的"本机知识"：每个包在磁盘上长什么样、怎么探测系统里已有的那一个。
//
// 这一整份**故意不来自服务端清单**。清单能决定的只有"下哪个文件"（id / 版本 /
// 直链 / sha256），"本机要执行哪个文件"由这里写死。把 exe 相对路径放进清单看着更
// 灵活，实际是把"决定本机执行什么"的权力交了出去——而这个包的用途正是给 agent 一个
// 解释器，被换掉的后果比安装包被换掉还直接。代价是新增一个组件要发一次版；那本来
// 也要改 UI 文案与探测逻辑，不是白白多出来的一次。

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/protocol"
)

// packSpec 是一个运行时包的静态定义。
type packSpec struct {
	// id 是 protocol.RuntimePack* 之一，也是磁盘上的目录名。
	id string
	// label / summary 是设置页那一行的标题与副标题。
	label, summary string
	// minVersion 是"系统自带的那个够不够用"的门槛，形如 "3.11"。
	//
	// 它只用来判系统的那一份：托管包的版本由我们自己发，不需要再判一次。
	minVersion string
	// exeNames 是探测用的命令名，按序尝试（不带 .exe）。顺序有讲究：Windows 上
	// "python" 比 "python3" 常见，类 Unix 上反过来。
	exeNames []string
	// binDirs 是包内要前置到 PATH 的目录（相对解压根，""=根目录本身）。
	binDirs func(goos string) []string
}

// packSpecs 是全部三个包，顺序即前端显示顺序。
//
// 按"缺了它有多要命"排：Python 是办公技能的刚需（公文、纪要、表格全靠它），Node
// 次之，Git 对办公场景几乎用不上——所以 Git 只在 Windows 发包，另外两个平台用系统的。
var packSpecs = []packSpec{
	{
		id:         protocol.RuntimePackPython,
		label:      "Python",
		summary:    "生成 Word / PPT / Excel 的技能要用它",
		minVersion: "3.11",
		exeNames:   []string{"python3", "python"},
		binDirs: func(goos string) []string {
			if goos == "windows" {
				// PBS 的 Windows 布局把 python.exe 放在根上，pip 装的命令行工具
				// 放 Scripts\ 里。两个都要，否则 pip 装完的东西调不起来。
				return []string{"", "Scripts"}
			}
			return []string{"bin"}
		},
	},
	{
		id:         protocol.RuntimePackNode,
		label:      "Node.js",
		summary:    "前端预览、部分 MCP 服务器要用它",
		minVersion: "18",
		exeNames:   []string{"node"},
		binDirs: func(goos string) []string {
			if goos == "windows" {
				return []string{""}
			}
			return []string{"bin"}
		},
	},
	{
		id:         protocol.RuntimePackGit,
		label:      "Git",
		summary:    "克隆仓库、看改动历史",
		minVersion: "2.0",
		exeNames:   []string{"git"},
		// MinGit 的自带布局：cmd\git.exe 是给外部调用的入口。
		// **不把 usr\bin 也加进去**：那里面是一整套 msys 工具（bash、ls、find……），
		// 前置到 PATH 会让 agent 在 Windows 上写的命令时而走 cmd 时而走 msys，
		// 而 Bash 工具默认用的是 cmd.exe——两种路径风格混在一起，错法千奇百怪。
		binDirs: func(string) []string { return []string{"cmd"} },
	},
}

// specFor 按 id 找定义。
func specFor(id string) (packSpec, bool) {
	for _, s := range packSpecs {
		if s.id == id {
			return s, true
		}
	}
	return packSpec{}, false
}

// packPublished 说明这个平台发不发这个包。
//
// Git 只有 Windows：Linux / macOS 没有官方绿色包，自己编一个静态 git 是过度工程，
// 那两个平台上 git 要么系统自带（macOS 的 Xcode 命令行工具），要么一句包管理器命令。
func packPublished(id, goos string) bool {
	return id != protocol.RuntimePackGit || goos == "windows"
}

// runtimePlatform 是请求清单用的平台键，与版本更新那条链路同形。
func runtimePlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// ---- 系统里已有的那一个 --------------------------------------------------

// systemFind 探测系统里的同名命令：路径、版本、够不够用。
//
// 三件事各自对应一次真实的坑：
//
//  1. **Windows 应用商店的占位 exe 不算数**。装了 Windows 10/11 但没装 Python 的
//     机器上，%LOCALAPPDATA%\Microsoft\WindowsApps\python.exe 是存在的——它不是
//     解释器，是个一运行就弹出应用商店的壳。LookPath 找得到它，于是"探测到 Python"
//     然后每条命令都莫名其妙地失败。按路径排除是唯一可靠的判别法。
//  2. **必须真的执行一次 --version**。只看文件在不在，遇到坏掉的安装（少 DLL、
//     少 site 目录）就会误报"可用"。
//  3. **要有超时**。这条探测跑在启动路径上，而一个卡住的 exe（等网络的包装脚本、
//     被杀毒软件按住的进程）会把它一起拖住。
func systemFind(ctx context.Context, s packSpec) (path, version string, usable bool) {
	for _, name := range s.exeNames {
		p, err := exec.LookPath(name)
		if err != nil || isStoreStub(p) {
			continue
		}
		v := probeVersion(ctx, p)
		if v == "" {
			// 找得到但问不出版本：当它不存在。报一个"有但不知道行不行"的状态，
			// 用户也做不了任何决定。
			continue
		}
		return p, v, versionAtLeast(v, s.minVersion)
	}
	return "", "", false
}

// isStoreStub 判断这是不是 Windows 应用商店的占位 exe。
func isStoreStub(p string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	return strings.Contains(strings.ToLower(filepath.ToSlash(p)), "/microsoft/windowsapps/")
}

// versionProbeTimeout 是问一次版本的上限。
const versionProbeTimeout = 4 * time.Second

// versionRe 从 --version 的输出里抠版本号。三个命令的输出格式不同
// （"Python 3.12.11" / "v22.17.0" / "git version 2.50.0.windows.1"），
// 但都能用"第一个 x.y[.z]"取到。
var versionRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// probeVersion 执行 <exe> --version 并抠出版本号；问不出来返回 ""。
func probeVersion(ctx context.Context, exePath string) string {
	ctx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, exePath, "--version").CombinedOutput() //nolint:gosec // 探测的是 LookPath 找到的系统命令，参数固定
	if err != nil && len(out) == 0 {
		return ""
	}
	// 用 CombinedOutput 是因为老版本 Python（2.x）把版本打在 stderr 上。这条链路
	// 上不该再有 2.x，但"版本太旧"恰恰是我们要如实报出来的东西。
	return versionRe.FindString(string(out))
}

// versionAtLeast 比较点分版本号，缺的段按 0 算（"3.12" >= "3.11" 为真）。
//
// 只比数字段：git 会报 "2.50.0.windows.1"，多出来的那段与"够不够新"无关。
func versionAtLeast(got, want string) bool {
	g, w := versionParts(got), versionParts(want)
	for i := range w {
		var gi int
		if i < len(g) {
			gi = g[i]
		}
		switch {
		case gi > w[i]:
			return true
		case gi < w[i]:
			return false
		}
	}
	return true
}

func versionParts(v string) []int {
	fields := strings.Split(v, ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			break // 遇到非数字段就停（"windows" 那段）
		}
		out = append(out, n)
	}
	return out
}

// finalizePack 是解压之后、写完整标记之前的收尾。
//
// 目前只有一件事，而它防的是一个只在 Windows 上出现、且极难归因的故障：模型写
// `python3 script.py` 是很自然的（类 Unix 上就该这么写），而 Windows 的 PBS 包里
// 只有 python.exe。PATH 上找不到 python3.exe 就会往后落到系统那份——很可能正是
// 应用商店那个占位 exe，于是用户看到的是"跑个脚本怎么弹出了微软商店"。
//
// 复制一份（约 100 KB 的启动器）就把这条路堵死了。失败不算错：包本身是好的，
// 提示词里推荐的也是 python 那个名字。
func finalizePack(id, dir string) {
	if id != protocol.RuntimePackPython || runtime.GOOS != "windows" {
		return
	}
	alias := filepath.Join(dir, "python3.exe")
	if _, err := os.Stat(alias); err == nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, "python.exe"))
	if err != nil {
		debugLog("runtime pack: no python.exe to alias: %v", err)
		return
	}
	if err := os.WriteFile(alias, data, 0o755); err != nil { //nolint:gosec // 包内的可执行文件
		debugLog("runtime pack: write python3.exe alias: %v", err)
	}
}
