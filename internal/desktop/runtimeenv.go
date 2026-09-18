package desktop

// 把装好的运行时接进 agent 的执行环境：PATH 前置 + 一段告诉模型"本机有什么"的提示词。
//
// 两个出口，缺一不可：
//
//   - **PATH**（toolEnv）——让 `python` 这条命令真的跑得起来。
//   - **提示词**（promptAppend）——让模型**知道**它跑得起来。少了这一半，模型面对
//     "生成一份公文"会先回一句"请先安装 Python"，而 Python 就在那儿。反过来，机器上
//     确实没有 Python 时也要说清楚，否则它会写出一个注定跑不了的脚本再来解释为什么。
//
// PATH **只**写进本进程（applyEnv），**不**放进 ToolEnv：
//
//   - ToolEnv 是会话构建那一刻拍下的快照（引擎把这张表原样挂到工具上下文上），而引擎
//     的 Bash 每跑一条命令都现读 os.Environ()。PATH 放进快照，就等于把这个对话的 PATH
//     冻在了开对话的那一刻：开对话时已经装了 Node、中途再装 Python，这个对话里就永远
//     找不到 python3——而"中途装"正是 install_runtime 工具（模型自己装，见
//     runtimetool.go）的常态。只写进程 PATH，装完那一刻所有会话、下一条命令就都看得见。
//   - ToolEnv 里只剩 PIP_* 这类只该给工具子进程看的、且不随安装变化的变量。

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"

	"github.com/wt68/runcode/internal/protocol"
	"github.com/wt68/runcode/internal/runtimetool"
)

// runtimeEnv 是一次计算出来的执行环境快照。
type runtimeEnv struct {
	// dirs 是要前置到 PATH 的目录，按 packSpecs 的顺序。
	dirs []string
	// vars 是额外的环境变量（只给工具子进程）。
	vars map[string]string
	// prompt 是附加到系统提示词后面的那一段（""=不附加）。
	prompt string
}

// basePath 是本进程启动时的 PATH。
//
// 每次都从它重算，而不是往当前 PATH 上再叠一层：装一次、卸一次、再装一次之后，
// 叠出来的 PATH 里会有三份互相遮挡的运行时目录，而排错时没有任何线索指向"是我们
// 自己叠的"。
var basePath = sync.OnceValue(func() string { return os.Getenv("PATH") })

// applyEnv 重算环境，写进本进程的 PATH，并存下快照供会话构建时取用。
func (m *runtimeManager) applyEnv() {
	e := m.buildEnv()
	m.env.Store(e)
	path := basePath()
	if len(e.dirs) > 0 {
		path = strings.Join(e.dirs, string(os.PathListSeparator)) + string(os.PathListSeparator) + path
	}
	if err := os.Setenv("PATH", path); err != nil {
		debugLog("runtime env: set PATH: %v", err)
	}
}

// envSnapshot 取最近一次算出来的环境；还没算过就现算一次。
func (m *runtimeManager) envSnapshot() *runtimeEnv {
	if e := m.env.Load(); e != nil {
		return e
	}
	e := m.buildEnv()
	m.env.Store(e)
	return e
}

// buildEnv 按当前状态算出执行环境。
func (m *runtimeManager) buildEnv() *runtimeEnv {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := &runtimeEnv{vars: map[string]string{}}
	var lines []string
	for _, s := range packSpecs {
		st := m.packs[s.id]
		switch {
		case st.stage == protocol.RuntimeStageReady && st.dir != "":
			for _, sub := range s.binDirs(runtime.GOOS) {
				e.dirs = append(e.dirs, filepath.Join(st.dir, sub))
			}
			lines = append(lines, managedLine(s, st))
		case st.sysUsable:
			lines = append(lines, "- "+s.label+" "+st.sysVersion+" is installed on this machine; run it as `"+promptCommand(s.id)+"`.")
		default:
			lines = append(lines, missingLine(s, st))
		}
	}
	// pip 的版本检查会往 stderr 上打一段升级提示，而工具结果里多出来的那几行会被
	// 模型当成"命令出了问题"。关掉它是纯收益。
	e.vars["PIP_DISABLE_PIP_VERSION_CHECK"] = "1"
	// 内网镜像：设了就透传给工具子进程。不做成设置项是因为它是部署环境的属性，
	// 同一台机器上所有会话都一样，而环境变量正是表达这种东西的地方。
	if idx := strings.TrimSpace(os.Getenv("RUNCODE_PIP_INDEX_URL")); idx != "" {
		e.vars["PIP_INDEX_URL"] = idx
	}
	e.prompt = promptSection(lines)
	return e
}

// managedLine 是"应用自带了这个"的那一行。
//
// 特意点明"随应用自带"：模型据此知道这份环境是可预期的（版本、预装库都定死了），
// 不必先去探测一遍再动手。
func managedLine(s packSpec, st *packState) string {
	line := "- " + s.label + " " + st.version + " is bundled with this app and ready to use; run it as `" + promptCommand(s.id) + "`."
	if libs := libsFromNotes(st.avail.Notes, s.label, st.version); libs != "" && s.id == protocol.RuntimePackPython {
		// 告诉模型预装了什么，它才不会为了一个已经在包里的库先去跑一次 pip install
		// ——而那在内网多半是跑不通的。
		line += " Preinstalled libraries: " + libs + "."
	}
	if st.needsAuthorize {
		// 麒麟安全中心还没放行它（见 kysec.go）。不说的话，模型看到的只是一个 126 退出码
		// 或"权限不够"，会去改脚本、换写法，绕半天圈子——而问题根本不在脚本里。
		line += " On this machine the system security center has not trusted it yet: every run pops up a confirmation on the user's desktop" +
			" and fails (exit code 126, \"permission denied\") if nobody accepts within 30 seconds. If that happens, do not rewrite the script;" +
			" call the " + runtimetool.Name + " tool for it again (that re-requests the authorization — the user types their password in the app's own dialog)," +
			" or, if you do not have that tool, ask the user to click 授权 next to it in 设置 → 运行时环境."
	}
	return line
}

// libsFromNotes 从清单的 notes 里取出"预装了什么"。
//
// notes 是给**人**看的一整句（"Python 3.12.11，含 python-docx / openpyxl"），前半截
// 与这一行自己已经打印过的名字和版本号重复。不剥掉的话提示词里会出现
// "Python 3.12.11 … Preinstalled libraries: Python 3.12.11，含 …"，同一个版本号说两遍。
//
// 剥不掉就原样用：notes 的措辞由服务端定，这里只处理它现在的形状，不做花哨的解析。
func libsFromNotes(notes, label, version string) string {
	out := strings.TrimSpace(notes)
	out = strings.TrimSpace(strings.TrimPrefix(out, label+" "+version))
	out = strings.TrimLeft(out, "，,：: ")
	out = strings.TrimSpace(strings.TrimPrefix(out, "含"))
	return strings.TrimSpace(out)
}

// missingLine 是"没有这个"的那一行，并说清楚该怎么办。
//
// **不要让模型叫用户自己去下载安装**：麒麟 V10 上升级系统 python3 会把 dnf 一起
// 搞挂，而用户照着模型的建议动手之后没人收得了场。正确的出口只有设置页那一个。
func missingLine(s packSpec, st *packState) string {
	line := "- " + s.label + " is NOT available"
	if st.sysVersion != "" {
		line += " (this machine has " + s.label + " " + st.sysVersion + ", which is too old — " + s.minVersion + "+ is required)"
	}
	// 陈述**后果**，而不是下禁令。"没有它 → 需要它的东西会失败"是事实，模型据此
	// 自己判断绕不绕得开；写成"不准写需要它的脚本"则是一条规则，而规则会连一个
	// 只有局部依赖它的任务也一起推掉。唯一保留的禁令在下面那条——那条防的是真实
	// 伤害（在麒麟上换掉系统 python3 会把 dnf 一起搞挂），不是效率问题。
	line += ", so anything that needs it will fail."
	if !packPublished(s.id, runtime.GOOS) {
		// 本平台根本不发这个包（Linux/macOS 的 Git 就是：没有官方绿色包，当初就
		// 决定走系统自带的）。这时候**不能**把用户指到设置页——那里只会显示
		// "本平台暂未提供安装包"，等于把人带进死胡同。
		return line + " This app cannot install it on this platform; the user can install it with the system package manager (e.g. sudo apt install git) and restart the app."
	}
	// 发了包但没装：出口是应用自己的安装器——模型经 install_runtime 工具调它（每次要用户
	// 批准，下的是校验过的那一份，装到用户目录、不提权），装完这个对话里立刻就能用。
	// **别让模型叫用户自己去下载安装**——在银河麒麟上升级系统 python3 会把 dnf 一起搞挂，
	// 而用户照做之后没人收得了场。
	//
	// 给了工具还必须说一句"别的装法都不行"：早先只说"别让用户自己装"时，模型在麒麟真机上
	// 把它读成了"那我替用户装"——花了十五轮去找别的 Python、测技能脚本能不能在 3.8 上凑合
	// 跑，最后执行了 `curl … astral.sh/uv/install.sh | sh`。自己下的解释器没有预装库、应用管
	// 不到，在开着执行控制的麒麟上还会被安全中心直接拦下。
	//
	// "没有这个工具时"那半句不是摆设：这段提示词也会进子代理，而子代理拿不到 ExtraTools
	// 注册的工具（见 configureSession）。不给它退路，它会去编一个不存在的工具调用。
	line += " The app ships its own copy: when the task needs it, call the " + runtimetool.Name + " tool with runtime \"" + s.id + "\"" +
		" (the user approves the call; it downloads the app's verified copy into the user's own directory, no admin rights)," +
		" then continue — it works in this conversation as soon as the tool succeeds." +
		" If you do not have that tool, stop and ask the user to click 安装 next to it in 设置 → 运行时环境." +
		" Do not install it any other way (uv, pyenv, conda, `curl … | sh`, downloading an interpreter) and do not tell the user to download it."
	if s.id == protocol.RuntimePackPython {
		// 这条危害是 Python 独有的，别套到 Node/Git 头上：银河麒麟等发行版的
		// dnf/apt 工具链就架在系统 python3 上，用户照着"去装个新版 Python"动手
		// 很可能把包管理器一起弄坏，而那种事故现场没人收得了。
		line += " On systems like 银河麒麟 this matters: replacing the system Python breaks the OS package manager."
	}
	return line
}

// promptCommand 是提示词里推荐给模型的命令名。
//
// Windows 上推荐 python 而不是 python3：包里确定有 python.exe，而 python3 这个名字
// 在 Windows 上另有一个陷阱（应用商店的占位 exe），能不提就不提。
func promptCommand(id string) string {
	switch id {
	case protocol.RuntimePackPython:
		if runtime.GOOS == "windows" {
			return "python"
		}
		return "python3"
	case protocol.RuntimePackNode:
		return "node"
	default:
		return "git"
	}
}

// promptSection 把几行拼成附加到系统提示词后面的那一段。
func promptSection(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Runtime environment\n\n")
	// 声明优先级：项目记忆会被原样带进每一个新对话，而它可能写于这些功能出现之前（麒麟
	// 真机上就有一条"需先用户态安装独立 Python（如 uv）"的旧记忆，模型每次都照着它去绕）。
	b.WriteString("Interpreters and tools available to the Bash tool on this machine. This reflects the machine's current state" +
		" and supersedes older notes (for example in memory) about missing or unusable interpreters:\n\n")
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}

// toolEnv 是注入每个工具子进程的环境（engine.Config.ToolEnv）。
//
// **故意不含 PATH**：ToolEnv 是会话构建时的快照，PATH 放进来就冻住了（见文件头）。
// 运行时目录经 applyEnv 写进本进程的 PATH，工具子进程每次启动时继承的正是它。
func (m *runtimeManager) toolEnv() map[string]string {
	e := m.envSnapshot()
	out := make(map[string]string, len(e.vars))
	for k, v := range e.vars {
		out[k] = v
	}
	return out
}

// promptAppend 是附加给模型的那一段。
func (m *runtimeManager) promptAppend() string { return m.envSnapshot().prompt }

// runtimeProbeTimeout 是启动时那趟系统探测的总上限（三个命令，各自还有自己的
// 4 秒上限）。给整趟再设一道闸是因为超时只挡得住"卡住"，挡不住"三个都很慢"。
const runtimeProbeTimeout = 30 * time.Second

// applyRuntimeEnv 把运行时环境接进一条会话的配置（configureSession 调它）。
//
// 两处都用**合并**而不是覆盖：ToolEnv 与 SystemPromptAppend 都是公共的口子，今天
// 没有别人在用，但"我先到所以我说了算"这种写法会在下一个功能接进来的那天悄悄
// 生效——那时候丢掉的是别人的设置，而且不报错。
func applyRuntimeEnv(m *runtimeManager, cfg *engine.Config) {
	if m == nil || cfg == nil {
		return
	}
	mergeToolEnv(cfg, m.toolEnv())
	appendSystemPrompt(cfg, m.promptAppend())
}

// mergeToolEnv 把 env 并进 cfg.ToolEnv（同名覆盖）。合并而不是整个替换：ToolEnv 是
// 公共口子，运行时环境与 sudo 都往里放东西，谁后到谁覆盖整张表就会悄悄丢掉另一个。
func mergeToolEnv(cfg *engine.Config, env map[string]string) {
	if cfg == nil || len(env) == 0 {
		return
	}
	if cfg.ToolEnv == nil {
		cfg.ToolEnv = make(map[string]string, len(env))
	}
	for k, v := range env {
		cfg.ToolEnv[k] = v
	}
}

// appendSystemPrompt 在 cfg.SystemPromptAppend 后面接一段（理由同上：合并，不覆盖）。
func appendSystemPrompt(cfg *engine.Config, section string) {
	if cfg == nil || strings.TrimSpace(section) == "" {
		return
	}
	if strings.TrimSpace(cfg.SystemPromptAppend) == "" {
		cfg.SystemPromptAppend = section
		return
	}
	cfg.SystemPromptAppend = strings.TrimRight(cfg.SystemPromptAppend, "\n") + "\n\n" + section
}
