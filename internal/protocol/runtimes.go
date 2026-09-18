package protocol

// 运行时环境包（Python / Node / Git）——桌面外壳自己的功能，与引擎无关。
//
// 解决的问题：用户的机器上不一定有 Python，装了的也不一定够新（银河麒麟 V10 是
// 3.8，macOS 是 Xcode 命令行工具那份 3.9/3.10），而办公技能要 3.11+ 才跑得起来。
// 让用户自己去装是不现实的——**麒麟上升级系统 python3 会把 dnf 和一堆系统工具
// 一起搞挂**，那不是"用户多点两下"的事，是现场没法收场的事故。
//
// 于是：平台发一份绿色包，应用下到用户目录里解开，只在**工具子进程**的 PATH 前面
// 加上它。三条纪律各自对应一次真实的坑：
//
//  1. **全程不提权**。不装进 Program Files、不写系统 PATH、不碰注册表。安装器是
//     admin 跑的，装进去的东西用户只读——那样 pip install 会当场失败，而且失败得
//     莫名其妙。落在用户目录里，pip 才有地方写。
//  2. **只改工具子进程的环境，不改本进程、更不改系统**。用户自己终端里的 python
//     还是他原来那个，我们只是给 agent 准备了一份。
//  3. **"跑什么"写死在客户端，清单只说"下什么"**。清单给 id/版本/直链/sha256，
//     可执行文件在包里的相对路径由客户端按 id 与平台决定（见 desktop/runtimepack.go）。
//     反过来把 exe 路径放进清单，等于让服务端指定本机要执行哪个文件。
//
// 与版本更新（update.go）共用的形状：后端每次状态变化发**整份** RuntimeInfo，前端
// 只做它的镜子。理由同 UpdateInfo——按阶段各发一个事件，前端就得自己把它们拼回一个
// 状态，而拼错的那几种最难查。

// 运行时包的 id。它同时是清单里的 id、磁盘上的目录名与前端的行键，因此是稳定字符串。
const (
	// RuntimePackPython 是 Python 运行时（含预装的 python-docx / python-pptx /
	// openpyxl 等办公依赖——只发一个裸解释器的话，用户只是把"没有 Python"的
	// 困扰换成了"pip 装不上包"的困扰，内网尤其如此）。
	RuntimePackPython = "python"
	// RuntimePackNode 是 Node.js 运行时（含 npm）。
	RuntimePackNode = "node"
	// RuntimePackGit 是 Git。**只有 Windows 有**：Linux/macOS 没有官方绿色包，
	// 自己编一个静态 git 属于过度工程，那两个平台走系统自带的。
	RuntimePackGit = "git"
)

// 托管包的安装阶段。
//
// 它只描述**托管包**的状态，不掺和"系统里有没有"——后者是 RuntimePack.System* 那
// 几个字段。两件正交的事各自表达，前端才画得出"系统有 3.8（太旧），托管包未安装"
// 这种真实存在的组合；塞进一个枚举里就只能二选一地撒谎。
const (
	// RuntimeStageAbsent 是没装托管包（也包括装过又被删掉的）。
	RuntimeStageAbsent = "absent"
	// RuntimeStageDownloading 正在下载，Received/Size 是真实字节数。
	RuntimeStageDownloading = "downloading"
	// RuntimeStageVerifying 正在校验 sha256。单独一个阶段而不是并进下载：几百 MB
	// 的包校验要几秒，进度条停在 100% 不动时得说得出自己在干什么。
	RuntimeStageVerifying = "verifying"
	// RuntimeStageExtracting 正在解压。它比下载还慢（几万个小文件），没有这个阶段
	// 用户会以为卡死了。
	RuntimeStageExtracting = "extracting"
	// RuntimeStageAuthorizing 正在请麒麟安全中心放行（KYSEC 执行控制开着时，解压出的
	// 程序不在白名单里，每次运行都会弹框）。这一步会弹应用的密码框，所以单列一个阶段，
	// 好让界面说清楚"现在在等你输密码"。
	RuntimeStageAuthorizing = "authorizing"
	// RuntimeStageReady 已装好可用。
	RuntimeStageReady = "ready"
	// RuntimeStageFailed 这一轮失败了，Error 是给用户看的原因。
	RuntimeStageFailed = "failed"
)

// EventRuntimes carries a RuntimeInfo whenever any pack's state changes.
const EventRuntimes = "runtimes:status"

// RuntimeInfo 是运行时环境的完整状态，也是唯一的对外形状。
//
// 它同时是 RuntimeStatus()（纯读，不联网）的返回值和 EventRuntimes 的载荷。
type RuntimeInfo struct {
	// Platform 是本机的平台键（"windows/amd64"），与清单请求里的那个一致。
	// 露出来是为了让"这个平台还没发布运行时包"这种情况说得清楚。
	Platform string `json:"platform"`
	// Packs 是本平台该有的包，顺序固定（python、node、git），缺发布的也在列——
	// 前端按它画一行行，"平台没发这个包"也是一种要显示的状态。
	Packs []RuntimePack `json:"packs"`
	// Fetched 表示本次运行取过清单了（无论成功与否）。没取过时前端画"检查中"，
	// 而不是画"全都没有"。
	Fetched bool `json:"fetched"`
	// Error 是取清单失败的原因（给用户看的话）；成功时为空。
	//
	// 取清单失败**不影响已装的包**：那些是磁盘上的既成事实，离线时照样能用。
	Error string `json:"error"`
}

// RuntimePack 是一个运行时包此刻的全部状态：托管包装没装、系统里有没有、能不能用。
type RuntimePack struct {
	// ID 是上面三个常量之一。
	ID string `json:"id"`
	// Label 是给人看的名字（"Python"）。
	Label string `json:"label"`
	// Summary 一句话说明装了它能干什么，用于设置页的副标题。
	Summary string `json:"summary"`
	// Stage 是托管包的阶段，上面六个常量之一。
	Stage string `json:"stage"`
	// Version 是**已装**托管包的版本（stage=ready 时有值）。
	Version string `json:"version"`
	// Available 是清单里那份的版本。与 Version 不同就是"有更新"；Version 为空
	// 而它有值就是"可安装"；两个都空是"这个平台没发布"。
	Available string `json:"available"`
	// Size 是待下载包的字节数（清单声明的），下载开始前就有，所以"要下多大"在
	// 用户点之前就能显示。
	Size int64 `json:"size"`
	// Received 是已下载字节数，只在 downloading 阶段有意义。
	Received int64 `json:"received"`
	// Error 是这一轮失败的原因（stage=failed 时）。
	Error string `json:"error"`
	// SystemPath 是系统里找到的同名可执行文件的绝对路径（""=没找到）。
	// 它与托管包无关，是"用户本来就有的那个"。
	SystemPath string `json:"systemPath"`
	// SystemVersion 是探测到的版本号（"3.8.10"），探不出来就为空。
	SystemVersion string `json:"systemVersion"`
	// SystemUsable 表示系统那份**够用**（版本达到 MinVersion）。麒麟 V10 的
	// python3 存在但是 3.8，这里就是 false——"有"和"够用"必须分得开，否则用户
	// 会看着一个绿勾却跑不起技能。
	SystemUsable bool `json:"systemUsable"`
	// MinVersion 是这个组件的最低版本要求，用于把"太旧"说成一句人话。
	MinVersion string `json:"minVersion"`
	// Active 是当前真正会被 agent 用到的那一个："managed"（托管包）、
	// "system"（系统自带）或 ""（两个都没有，功能不可用）。
	Active string `json:"active"`
	// NeedsAuthorize 为真表示麒麟安全中心还没放行这个托管包：每次运行都会在桌面弹一个
	// 安全框，30 秒没人点就失败。前端据此显示警告与「授权」按钮（AuthorizeRuntime）。
	//
	// 它会在两种情况下为真：装的时候用户取消了密码框；或者之后 pip 装了带 C 扩展的新
	// 库——那些新文件同样不在白名单里。
	NeedsAuthorize bool `json:"needsAuthorize"`
}

// ---- 服务端清单 ----------------------------------------------------------

// RuntimeManifest 是清单响应：这个平台上有哪些包可装。
//
//	GET {Bridge}/api/app/runtimes?product=xrun&platform=windows/amd64
//
// 为什么这一份 wire 类型是公开的、而版本更新那份（releaseWire）是私有的：清单
// 在本仓有**两个**消费者——客户端解析它，tools/runtimepacks 生成它。两处各写一遍
// 结构体，迟早有一天字段名对不上，而那种错只在装机之后才显形。放在这里，两边
// 编译期就绑住了。
type RuntimeManifest struct {
	// Platform 回显请求的平台键，便于排查"服务端按什么平台答的"。
	Platform string `json:"platform"`
	// Packs 是该平台可装的包。服务端没发布某个组件就不出现在列表里——那和
	// "发布了但下载失败"是两回事，前端的文案也不同。
	Packs []RuntimeManifestPack `json:"packs"`
}

// RuntimeManifestPack 是清单里的一个包。
//
// 它刻意**不含**可执行文件路径、PATH 目录、环境变量：那些是"本机要执行什么"，
// 由客户端按 id 与平台自己决定（见 desktop/runtimepack.go）。清单能改的只有
// "下哪个文件"，不能改"跑哪个文件"。
type RuntimeManifestPack struct {
	// ID 是 RuntimePack* 之一。客户端不认识的 id 一律忽略（老客户端遇到新组件时
	// 该安静地少一行，而不是画一个点了没反应的按钮）。
	ID string `json:"id"`
	// Version 是这份包的版本号（"3.12.11"），也是磁盘上的目录名。
	Version string `json:"version"`
	// URL 是包的直链（对象存储）。
	URL string `json:"url"`
	// SHA256 是包的十六进制摘要，**必填，缺了客户端拒绝下载**。
	//
	// 理由同版本更新：清单走 https 可信，但包的直链是清单里给的任意地址、下载那趟
	// 按设计不带鉴权头。没有校验就意味着谁在这条路上都能替换掉用户即将执行的
	// python.exe——而这个包的用途正是"给 agent 一个解释器"，被换掉的后果比安装器
	// 被换掉还直接。
	SHA256 string `json:"sha256"`
	// Size 是字节数，用于下载前显示"要下多大"。
	Size int64 `json:"size"`
	// Notes 是可选的说明（"含 python-docx / python-pptx / openpyxl"）。
	Notes string `json:"notes"`
}
