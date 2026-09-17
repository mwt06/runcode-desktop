package main

import "fmt"

// ---- 上游版本表 ----------------------------------------------------------
//
// 打包前**先核对一遍上游还有没有这几个 tag**：三个上游的发布节奏都不归我们管，
// 写死的版本号迟早会 404。工具会把完整 URL 打出来，404 时照着去上游对一眼即可。
//
// 为什么 pin 死一个 Python 版本、而不是"按技能声明的版本各装各的"：后者等于在应用
// 里再实现一个包管理器（多版本共存、按技能匹配、磁盘回收），收益只是让技能作者少写
// 一行兼容代码。代价是将来某个技能要 3.13 独有特性时得整体升一次——那是一次服务端
// 清单改动，可以接受。
//
// 选 3.12 不选 3.13：满足技能要求的 3.11+，同时 C 扩展轮子的覆盖率最稳。
const (
	// pythonVersion 是发布给用户的 Python 版本，也是包名与 site-packages 路径的一部分。
	pythonVersion = "3.12.11"
	// pythonABI 是 pip 下载轮子时用的 ABI 标签，必须与 pythonVersion 的大版本一致。
	pythonABI = "cp312"
	// pbsRelease 是 astral-sh/python-build-standalone 的发布 tag（一个日期）。
	//
	// 为什么用它而不是 python.org 的 embeddable zip：embeddable 没有 pip，且用
	// ._pth 关掉了 site 机制，装第三方包要 get-pip 加改配置文件；而 PBS 是完整
	// CPython、可重定位、**静态链接 OpenSSL**——最后一条正好绕开麒麟 V10 上
	// openssl 太老、pip 连 https 直接报 SSL 错那个坑。
	pbsRelease = "20250612"

	// nodeVersion 是 Node.js 的 LTS 版本（带 v 前缀的目录名在 URL 里另拼）。
	nodeVersion = "22.17.0"

	// gitVersion 是 Git for Windows 的版本，取它的 MinGit 资产——那是上游专门
	// 为"嵌进别的应用"发布的最小包，解压即用、不写注册表。
	gitVersion = "2.50.0"
)

// pythonWheels 是随 Python 包一起预装的第三方库。
//
// **这一层才是重点**：只发一个裸解释器的话，用户不过是把"没有 Python"的困扰换成了
// "pip 装不上包"的困扰——内网机器大概率连不上 PyPI，那时候他连换都没得换。
//
// 列表刻意保持短：每加一个都是几 MB 乘以六个平台。pandas/numpy 没进来是个有意识的
// 取舍（两个加起来约 30 MB，而用到它们的场景目前只有"数据分析"一类），要加就在这里
// 加，别在别处另开一条安装路径。
var pythonWheels = []string{
	"python-docx", // 生成/读取 .docx（公文、会议纪要、工作汇报这几条主线都靠它）
	"python-pptx", // 生成 .pptx
	"openpyxl",    // 读写 .xlsx
	"Pillow",      // python-pptx 处理图片要它；单列出来是因为它是二进制轮子，平台标签错了会静默装错
}

// ---- 平台矩阵 ------------------------------------------------------------

// target 是一个"平台 × 组件"的打包目标。
type target struct {
	// platform 是平台键，与客户端请求清单时用的那个一致（"windows/amd64"）。
	platform string
	// goos/goarch 拆开只是为了拼上游 URL，客户端不认这两个。
	goos, goarch string
}

// targets 是全部六个平台。麒麟 V10 与 V11 在这里是同一行（都是 linux/amd64 或
// linux/arm64）——运行时包与 WebKit ABI 无关，不像外壳那样必须分两份。
var targets = []target{
	{platform: "windows/amd64", goos: "windows", goarch: "amd64"},
	{platform: "windows/arm64", goos: "windows", goarch: "arm64"},
	{platform: "linux/amd64", goos: "linux", goarch: "amd64"},
	{platform: "linux/arm64", goos: "linux", goarch: "arm64"},
	{platform: "darwin/amd64", goos: "darwin", goarch: "amd64"},
	{platform: "darwin/arm64", goos: "darwin", goarch: "arm64"},
}

// ---- 每个组件的上游地址与包内布局 ----------------------------------------

// source 描述"从哪儿取、取下来长什么样"。
type source struct {
	// url 是上游直链。
	url string
	// sumURL 是上游校验和文件的地址（""=上游没提供）。有就必须对上：这一步是在
	// 我们这边做的，用户拿到的包里那个 sha256 是**重打包之后**算的，对不上上游
	// 就说明源头已经不是上游那份了。
	sumURL string
	// sumKind 说明 sumURL 指向的文件怎么解析："bare"=整个文件就是一个哈希，
	// "shasums"=每行 "<hash>  <filename>" 的清单。
	sumKind string
	// strip 是上游归档里要剥掉的顶层目录（"python/"、"node-v22.17.0-win-x64/"）。
	// 剥在打包时做，客户端才不必知道每个上游各自的目录习惯。
	strip string
	// version 是写进清单的版本号。
	version string
	// pipPlatform 是给这个包预装轮子时用的 pip 平台标签（""=这个包不装轮子）。
	//
	// 它跟着**解释器**走而不是跟着目标平台走，这一条是有代价换来的：Windows ARM64
	// 上装的其实是 x64 解释器（见 pythonSource），那时候轮子也必须是 win_amd64 的，
	// 否则包能装上、一 import lxml 就炸。两者绑在同一个结构体里，就不可能只改一处。
	pipPlatform string
}

// sourceFor 返回某个组件在某个平台上的上游来源；ok=false 表示这个平台不发这个组件。
func sourceFor(id string, t target) (source, bool) {
	switch id {
	case packPython:
		return pythonSource(t)

	case packNode:
		arch, ok := nodeArch(t)
		if !ok {
			return source{}, false
		}
		dir := "https://nodejs.org/dist/v" + nodeVersion + "/"
		// Linux/macOS 取 .tar.gz 而不是上游同时提供的 .tar.xz：xz 要额外的解压库，
		// 而本工具只用标准库。体积差几 MB，不值得为它引一个依赖。
		ext := ".tar.gz"
		if t.goos == "windows" {
			ext = ".zip"
		}
		name := fmt.Sprintf("node-v%s-%s%s", nodeVersion, arch, ext)
		return source{
			url: dir + name, sumURL: dir + "SHASUMS256.txt", sumKind: "shasums",
			strip: fmt.Sprintf("node-v%s-%s/", nodeVersion, arch), version: nodeVersion,
		}, true

	case packGit:
		// Git 只发 Windows。Linux/macOS 没有官方绿色包，自己编一个静态 git 属于
		// 过度工程——那两个平台上 git 要么系统自带（macOS 的 Xcode 命令行工具），
		// 要么一句 apt install 的事，而办公场景本来也几乎不用它。
		if t.goos != "windows" {
			return source{}, false
		}
		arch := "64-bit"
		if t.goarch == "arm64" {
			arch = "arm64"
		}
		name := fmt.Sprintf("MinGit-%s-%s.zip", gitVersion, arch)
		return source{
			// MinGit 没有单独的校验和文件（上游只在 release 页面正文里列），所以
			// 这里靠"下一次打包出来的 sha256 应当和上一次一致"来兜底。
			url:   "https://github.com/git-for-windows/git/releases/download/v" + gitVersion + ".windows.1/" + name,
			strip: "", version: gitVersion,
		}, true
	}
	return source{}, false
}

// pythonSource 给出 Python 包的上游来源，外加它对应的轮子平台标签。
//
// **Windows ARM64 发的是 x64 那份**，这是本文件里唯一一处有意的"名不副实"：
// python-build-standalone 至今没有 aarch64-pc-windows-msvc 的 install_only 资产
// （打包时实测 404），而 Windows 11 on ARM 能靠 x64 仿真跑它。取舍是"慢一点但能用"
// 对上"这类机器上办公技能整个不可用"，前者明显更好。代价写在这里而不是藏在别处：
// 轮子也必须跟着变成 win_amd64。
//
// 上游哪天补上了 ARM64 资产，把 triple 改回 aarch64-pc-windows-msvc、pip 标签改回
// win_arm64 即可，两处在同一个 case 里，不会只改一半。
func pythonSource(t target) (source, bool) {
	triple, pipTag := "", ""
	switch t.platform {
	case "windows/amd64":
		triple, pipTag = "x86_64-pc-windows-msvc", "win_amd64"
	case "windows/arm64":
		triple, pipTag = "x86_64-pc-windows-msvc", "win_amd64" // 见上：x64 仿真
	case "linux/amd64":
		// 不取 x86_64_v2/v3 那几个变体：它们要求较新的指令集，而麒麟 V10 的机器
		// 型号跨度很大（含兆芯等），基线版跑得到的地方最多。
		triple, pipTag = "x86_64-unknown-linux-gnu", "manylinux2014_x86_64"
	case "linux/arm64":
		triple, pipTag = "aarch64-unknown-linux-gnu", "manylinux2014_aarch64"
	case "darwin/amd64":
		// 标签写 11_0 而不是更老的 10_9，方向与直觉相反、但**必须如此**：pip 把
		// --platform macosx_X_Y 展开成"从 X.Y 一路往下"的兼容列表，所以请求 11.0
		// 能收下标着 10.13 的轮子（老系统编的，新系统跑得动），请求 10.9 反而会把
		// 它排除掉。实测就栽在这儿：Pillow 的 mac x86_64 轮子标的是
		// macosx_10_13_x86_64，写 10_9 时 pip 一句"No matching distribution"。
		//
		// 代价是包的下限跟着变成 macOS 11（Big Sur，2020 年），与 arm64 那一行的
		// 下限一致——arm64 本来就从 11.0 起步。
		triple, pipTag = "x86_64-apple-darwin", "macosx_11_0_x86_64"
	case "darwin/arm64":
		triple, pipTag = "aarch64-apple-darwin", "macosx_11_0_arm64"
	default:
		return source{}, false
	}
	// 取 install_only_stripped 而不是 install_only：两者的差别只是调试符号，而
	// 符号表对"跑技能脚本"毫无用处，体积差却很大（Linux 105 MB → 34 MB，Windows
	// 42 MB → 21 MB）。内网带宽下这三倍是用户等 2 分钟还是等 6 分钟的区别。
	name := fmt.Sprintf("cpython-%s+%s-%s-install_only_stripped.tar.gz", pythonVersion, pbsRelease, triple)
	base := "https://github.com/astral-sh/python-build-standalone/releases/download/" + pbsRelease + "/" + name
	return source{
		url: base, sumURL: base + ".sha256", sumKind: "bare",
		strip: "python/", version: pythonVersion, pipPlatform: pipTag,
	}, true
}

// nodeArch 把平台键翻译成 nodejs.org 的资产名片段。
func nodeArch(t target) (string, bool) {
	switch t.platform {
	case "windows/amd64":
		return "win-x64", true
	case "windows/arm64":
		return "win-arm64", true
	case "linux/amd64":
		return "linux-x64", true
	case "linux/arm64":
		return "linux-arm64", true
	case "darwin/amd64":
		return "darwin-x64", true
	case "darwin/arm64":
		return "darwin-arm64", true
	}
	return "", false
}

// sitePackages 是包内 site-packages 的相对路径（剥掉顶层目录之后）。预装的轮子
// 直接落在这儿，运行期就不需要 PYTHONPATH——标准位置本来就会被搜索到。
func sitePackages(t target) string {
	if t.goos == "windows" {
		return "Lib/site-packages/"
	}
	return "lib/python" + majorMinor(pythonVersion) + "/site-packages/"
}

// majorMinor 取 "3.12.11" 的 "3.12"。
func majorMinor(v string) string {
	dots := 0
	for i, c := range v {
		if c == '.' {
			dots++
			if dots == 2 {
				return v[:i]
			}
		}
	}
	return v
}
