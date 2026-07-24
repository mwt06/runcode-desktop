package desktop

// 智能(judge)模式的 harm 判定原本只看命令行本身:`python foo.py` 它看到的就是这
// 一行,看不到 foo.py 里是什么。而脚本往往是模型自己刚写的——一个看似人畜无害的
// `python build.py` 里可以藏任意行为,命令行级的判定拦不住。
//
// 这里在判定前有条件地把被执行的脚本内容读进来,拼进 harm 判定的 UNTRUSTED 段
// (引擎会把整段围栏成"不可信数据",见 buildHarmContent)。脚本是模型可控的,所以
// 只能进 untrusted,绝不能进 facts。
//
// "有条件"指:必须是已知解释器在跑一个带脚本扩展名的本地文件;文件必须落在工作区
// 内(经 resolveWithinWorkspace 兜底,越界/软链逃逸/不存在一律读不到);内容有大小
// 上限;二进制文件只记一行说明不塞内容。任一条件不满足就退回原来的"只看命令行",
// 那是安全的降级,不是失败。

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxHarmScriptBytes 限制读进 harm 判定的脚本字节数:够判定意图,又不至于把一个
// 生成的大文件灌进判定请求、抬高延迟与成本。超出的截断并标注。
const maxHarmScriptBytes = 64 << 10

// scriptInterpreters 是"命令行第一个词若是它、且后面跟着脚本文件,就值得连内容一起
// 判定"的解释器集合。故意只收会执行整份脚本文件的解释器;像 cat/grep 这类不在内
// (它们不执行代码,归读操作)。
var scriptInterpreters = map[string]bool{
	"python": true, "python3": true, "py": true,
	"node": true, "nodejs": true, "deno": true, "bun": true,
	"ruby": true, "perl": true, "php": true, "lua": true,
	"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true,
	"rscript": true, "pwsh": true, "powershell": true,
	"tsx": true, "ts-node": true,
}

// scriptExtensions 是被当作"脚本文件"的扩展名。用扩展名而不是"第一个非 flag 参数"
// 来定位脚本,恰好绕开了 flag 取值的歧义:`python -X utf8 app.py` 里 utf8 没有脚本
// 扩展名会被跳过,app.py 才被认出;`python -c "code"` 里那段内联代码同样没有扩展名
// (内联代码本就明摆在命令行上,判定看得到,无需再读文件)。
var scriptExtensions = map[string]bool{
	".py": true, ".pyw": true, ".js": true, ".mjs": true, ".cjs": true,
	".ts": true, ".mts": true, ".cts": true, ".tsx": true, ".jsx": true,
	".rb": true, ".pl": true, ".pm": true, ".php": true, ".lua": true,
	".sh": true, ".bash": true, ".zsh": true, ".r": true, ".ps1": true,
}

// scriptInvocation 从命令行认出"某解释器在跑某个本地脚本文件",返回脚本路径(命令
// 行里的原样,可能相对工作区)。纯函数,可单测。
//
// 不认(返回 ok=false)的情形:含 shell 组合/替换(| & ; ` $()——那时"跑的是哪个
// 文件"本身就不确定,且分类器已把这类判为高风险);第一个词不是已知解释器;或没有
// 任何带脚本扩展名的参数(REPL、`-c`/`-m` 内联代码、纯 stdin)。
func scriptInvocation(command string) (script string, ok bool) {
	if hasShellComposition(command) {
		return "", false
	}
	tokens := tokenizeQuoted(command)
	if len(tokens) < 2 {
		return "", false
	}
	if !scriptInterpreters[interpreterName(tokens[0])] {
		return "", false
	}
	for _, tok := range tokens[1:] {
		if tok == "" || strings.HasPrefix(tok, "-") {
			continue // flag(以及 flag 的取值,靠下面的扩展名判定滤掉)
		}
		if scriptExtensions[strings.ToLower(filepath.Ext(tok))] {
			return tok, true
		}
	}
	return "", false
}

// resolveScriptWithinWorkspace 是这条路径专用的边界关卡:唯一越界读取的防线。
//
// 它不能直接用 resolveWithinWorkspace,因为那个函数的契约是"传入工作区相对路径"
// (前端调用方给的都是相对路径),而这里的脚本路径来自命令行,可能是绝对路径。命令
// 以工作区为工作目录运行,所以相对路径就是工作区相对;绝对路径先换算成工作区相对
// 再交给同一套包含性校验——`python D:\ws\app.py`(工作区内的绝对写法)本该能读,不
// 该因为 Join 拼出垃圾路径而被漏掉。换算不出(如 Windows 上跨盘符)或换算后越界的,
// 一律拒。工作区外的绝对路径到这里会得到 `..\..\x` 形式,被 resolveWithinWorkspace
// 的词法检查挡下,fail-closed 不变。
func resolveScriptWithinWorkspace(ws, script string) (string, error) {
	if filepath.IsAbs(script) {
		rel, err := filepath.Rel(ws, script)
		if err != nil {
			return "", err
		}
		script = rel
	}
	return resolveWithinWorkspace(ws, script)
}

// interpreterName 取命令首词的程序名:去目录、去 .exe、转小写,好让 `/usr/bin/python3`
// 和 `C:\Python\python.exe` 都归到 "python3" / "python"。
func interpreterName(tok string) string {
	name := strings.ToLower(filepath.Base(filepath.FromSlash(tok)))
	return strings.TrimSuffix(name, ".exe")
}

// hasShellComposition 报告命令是否含改变"执行什么"的 shell 组合或替换。故意不含
// 重定向 > <:`python gen.py > out` 里跑的仍是 gen.py,读它依然正确;重定向本身的
// 风险由分类器另行标记。
func hasShellComposition(command string) bool {
	return strings.ContainsAny(command, "|&;`") || strings.Contains(command, "$(")
}

// tokenizeQuoted 按空白切词,识别并剥去单/双引号,好让 `python "my scripts/app.py"`
// 切成一个词。与引擎分类器的 tokenizeCommand 同构,但那是 unexported,这里自带一份。
func tokenizeQuoted(command string) []string {
	var tokens []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range command {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return tokens
}

// harmScriptAddendum 在 harm 判定前,把 command 要执行的本地脚本内容读成一段 untrusted
// 附文。不是脚本执行、脚本在工作区外、不存在、非普通文件或读失败,都返回 ""(退回只看
// 命令行的判定)。返回的文本会被拼进 describeAction 的 untrusted 段,由引擎统一围栏。
func (a *App) harmScriptAddendum(command string) string {
	rel, ok := scriptInvocation(command)
	if !ok {
		return ""
	}
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	if ws == "" {
		return ""
	}
	full, err := resolveScriptWithinWorkspace(ws, rel)
	if err != nil {
		return ""
	}
	info, err := os.Stat(full)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	f, err := os.Open(full) //nolint:gosec // G304: full 已经 resolveWithinWorkspace 限定在工作区内
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	// 多读 1 字节用来判断是否发生了截断。
	buf := make([]byte, maxHarmScriptBytes+1)
	n, err := io.ReadFull(f, buf)
	// ReadFull reports ErrUnexpectedEOF/EOF when the file is shorter than the
	// buffer — expected here, not a failure. Any other error means the read
	// itself failed, so add nothing.
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return ""
	}
	data := buf[:n]
	truncated := n > maxHarmScriptBytes
	if truncated {
		data = data[:maxHarmScriptBytes]
	}
	if bytes.IndexByte(data, 0) >= 0 {
		// 二进制文件塞进判定请求既无益又费 token,只记一行事实。
		return fmt.Sprintf("\n\nthe command runs the local file %q, which is binary — contents omitted.", rel)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n\nthe command runs the local script %q; its contents follow as untrusted data:\n", rel)
	b.Write(data)
	if truncated {
		fmt.Fprintf(&b, "\n…(truncated at %d KiB)", maxHarmScriptBytes>>10)
	}
	return b.String()
}
