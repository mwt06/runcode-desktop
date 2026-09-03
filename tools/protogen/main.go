// Command protogen generates the desktop frontend's TypeScript protocol layer
// from the Go single sources of truth. The wire contract is split across two Go
// packages by ownership — agentloop's protocol package (what the engine's host
// produces or consumes during a turn, shared with cmd/runcode-server) and
// internal/protocol (what this shell invents for its own features) — and they
// are read here as one contract, sorted by name, so moving a type between them
// leaves the generated output unchanged:
//
//   - both protocol packages → types.ts (interfaces for every wire struct, plus
//     the constant groups as const objects and literal-union types)
//   - both packages' Event* constants → events.ts (the EventMap and typed
//     subscription helpers over the Wails runtime event bus)
//   - internal/desktop's App exported methods → commands.ts (typed wrappers
//     for the Wails bindings, annotated with each command's CommandKind)
//
// Run it directly with `go run ./tools/protogen`. With --check it compares the
// would-be output against the files on disk and exits non-zero on drift (the
// CI gate); the default mode writes the files.
//
// Generation fails loudly when the sources drift from their metadata: an App
// method absent from protocol.CommandKinds (or a stale map entry), an Event*
// constant missing from the event→payload table, an App signature that leaks a
// non-protocol type onto the wire, or the same exported name declared by both
// protocol packages.
//
// # 中文速览
//
// protogen 是「协议代码生成器」:把 Go 侧的 wire 契约翻译成前端那份 TypeScript,
// 并在 CI 里守住两侧不漂移。它解决的是 Wails 前后端之间**编译器管不到的那条边**——
// Go 改了字段名、加了事件、换了命令签名,TS 侧不会报错,只会在运行期悄悄变成
// undefined;protogen 把这条边变成构建期错误。
//
// 整条数据流都在 run 函数里,四步:
//
//  1. packages.Load —— 用 go/packages 把三个包连同**类型信息**载入(不是文本解析,
//     拿到的是 go/types 的真实类型,所以能判断"这个字段是不是指针""这个常量是什么具名类型")。
//  2. extract(extract.go)—— 把三个包读成一份与语言无关的中间模型 model
//     (结构体 / 常量组 / 事件表 / 命令表),各道一致性校验也都在这一步做。
//  3. emitTypes / emitEvents / emitCommands(emit.go)—— 把 model 渲染成三份
//     TypeScript 源码,此时还只是内存里的 []byte。
//  4. 落盘或比对 —— --check 只比对(CI 门禁,有差异退出 1),默认模式写文件。
//
// 读源码建议顺序:main.go(本文件,流程骨架)→ extract.go(规则与各道校验)→ emit.go(模板)。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// The wire contract is defined by two packages, generated as one: the engine's
// protocol package owns what the host produces or consumes while a turn runs
// (and is shared with cmd/runcode-server), and internal/protocol owns what the
// desktop shell invents for its own features. Order matters only for error
// messages; the emitted TypeScript is sorted by name, so a type moving between
// the two packages does not move in the output.
//
// 中文:wire 契约由两个包共同定义,但生成时**当成一份读**。这里的先后只影响报错信息
// 的顺序,生成物一律按名字排序——所以把一个类型从引擎包挪到 internal/protocol(或反过来),
// 生成的 TS 一个字节都不会变。这正是 CLAUDE.md 里「加 DTO 加在本仓、不要加进引擎」
// 那条纪律能落地的前提。
var protocolPkgPaths = []string{
	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol",
	"github.com/wt68/runcode/internal/protocol",
}

// desktopPkgPath 是命令面的来源:桌面核心包里的 App 类型,它的每个导出方法都是一条
// 前端可调用的 Wails 命令(少数纯外壳接线的方法在 extract.go 的 excludedMethods 里排除)。
const desktopPkgPath = "github.com/wt68/runcode/internal/desktop"

// outRelDir is where the generated TypeScript lands, relative to the module root.
// 前端按分层重组后,生成物随 bridge 一起归入 core/ —— 改前端目录时务必同步这里,
// 否则 --check 门禁会对着一个不存在的旧路径报"漂移"。
var outRelDir = filepath.Join("cmd", "runcode-desktop", "frontend", "src", "core", "protocol")

func main() {
	// 两种模式共用同一套生成逻辑,区别只在最后一步是"写"还是"比对":本地改完协议跑
	// `go run ./tools/protogen` 写文件,CI 跑 `--check` 只验不写。用同一份代码产出与校验,
	// 是"校验器不可能与生成器不一致"的关键——否则守门的和干活的各说各话。
	check := flag.Bool("check", false, "compare generated output with the files on disk; exit 1 on drift instead of writing")
	flag.Parse()
	if err := run(*check); err != nil {
		// 生成器属于构建工具:出错就把原因打到 stderr 并以非 0 退出,让 CI / Make 直接断链,
		// 绝不"尽力而为地生成一半"——半份协议比没有协议更难排查。
		fmt.Fprintf(os.Stderr, "protogen: %v\n", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	// Mode 是按位或的"我需要哪些信息"清单,go/packages 据此决定加载多深(要得越多越慢)。
	// 这里每一项都有确切用途:
	//   NeedName      包名与导入路径 —— 把加载结果按路径分门别类
	//   NeedFiles     包内文件列表   —— 配合 NeedSyntax 找注释
	//   NeedImports   依赖图         —— 解析跨包引用到的类型
	//   NeedTypes     go/types 类型  —— 核心:字段是不是指针、常量是什么具名类型,全靠它
	//   NeedSyntax    语法树(AST)  —— 取 doc 注释(类型信息里不含注释)
	//   NeedTypesInfo AST ↔ 类型映射 —— 把注释挂回具体的类型 / 方法
	//   NeedModule    模块信息       —— 定位仓库根目录,决定输出写到哪
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports |
			packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo |
			packages.NeedModule,
	}
	// 一次性加载全部三个包(两个 protocol + 桌面核心)。一次 Load 比分三次快得多,更要紧的是
	// 三者共享同一份类型对象——同一个具名类型在三个包里是同一个指针,后面 typeMapper 才能用
	// "这个类型的包路径是否属于 protocol"这种身份判断,来识别"它到底能不能上 wire"。
	pkgs, err := packages.Load(cfg, append(append([]string{}, protocolPkgPaths...), desktopPkgPath)...)
	if err != nil {
		return fmt.Errorf("load packages: %w", err)
	}
	// packages.Load 的返回顺序不保证,且**包级错误不会体现在上面的 err 里**(那个 err 只报
	// "加载器本身挂了"),编译错误挂在每个包的 .Errors 上。所以这里一边按路径建索引,
	// 一边把包内错误收集起来。
	byPath := map[string]*packages.Package{}
	var loadErrs []string
	for _, p := range pkgs {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e.Error())
		}
		byPath[p.PkgPath] = p
	}
	// 带着不完整的类型信息继续生成,只会产出残缺的 TS(比如某个类型悄悄退化成 unknown),
	// 不如在这里一次把所有编译错误报全。
	if len(loadErrs) > 0 {
		return fmt.Errorf("packages failed to load:\n  %s", strings.Join(loadErrs, "\n  "))
	}
	// 按 protocolPkgPaths 的声明顺序取出,保证多次运行的报错信息稳定(生成物本身已排序,
	// 与这里的顺序无关)。
	protoPkgs := make([]*packages.Package, 0, len(protocolPkgPaths))
	for _, path := range protocolPkgPaths {
		p := byPath[path]
		if p == nil {
			// 走到这里通常意味着模块路径变了(引擎换仓库 / 换包名),而不是代码有语法错。
			return fmt.Errorf("load result is missing %s", path)
		}
		protoPkgs = append(protoPkgs, p)
	}
	deskPkg := byPath[desktopPkgPath]
	if deskPkg == nil {
		return fmt.Errorf("load result is missing %s", desktopPkgPath)
	}
	// The output anchors on the desktop package's module (the repo root, where
	// the frontend lives) — NOT the protocol package's module, which is the
	// external agentloop module.
	//
	// 中文:输出目录锚在**桌面包所属模块**的根上(即本仓,前端就在这个仓里),绝不能锚在
	// protocol 包上——引擎的 protocol 属于外部模块 agentloop,go.work 联动时它指向
	// ../agentloop,GOWORK=off 时更是指向 module cache 里的只读目录。锚错了要么把生成物
	// 写进隔壁仓库,要么写进只读缓存直接失败。
	if deskPkg.Module == nil || deskPkg.Module.Dir == "" {
		return fmt.Errorf("no module information for %s (protogen must run inside the runcode repo)", desktopPkgPath)
	}

	// 第 2 步:三个包 → 一份中立模型。所有一致性校验(命令清单交叉核对、事件表核对、
	// 签名是否泄漏非 wire 类型、两包重名……)都在 extract 里做,任一不过即整体失败。
	m, err := extract(protoPkgs, deskPkg)
	if err != nil {
		return err
	}

	// 第 3 步:模型 → 三份 TypeScript 源码。此刻只在内存里,尚未落盘——因为 --check 模式
	// 需要拿"本该生成的内容"与磁盘上的对比,写与不写共用同一份产出。
	files := map[string][]byte{
		"types.ts":    emitTypes(m),
		"events.ts":   emitEvents(m),
		"commands.ts": emitCommands(m),
	}
	outDir := filepath.Join(deskPkg.Module.Dir, outRelDir)

	// 第 4 步之一:--check(CI 门禁)。只比对不写盘,任何一份对不上就退出 1,
	// 把"改了协议忘了重新生成"变成一次构建失败,而不是上线后的运行期 undefined。
	if check {
		var stale []string
		for _, name := range sortedKeys(files) { // 排序遍历:报错列表的顺序也要稳定
			existing, err := os.ReadFile(filepath.Join(outDir, name))
			// 读不到(文件被删 / 输出路径改了)与内容不一致,都算"过期",共用一句报错——
			// 因为修复动作是同一个:重新跑一次生成。
			if err != nil || !bytes.Equal(normalizeEOL(existing), normalizeEOL(files[name])) {
				stale = append(stale, filepath.Join(outDir, name))
			}
		}
		if len(stale) > 0 {
			return fmt.Errorf("generated files are out of date (run `go run ./tools/protogen`):\n  %s",
				strings.Join(stale, "\n  "))
		}
		return nil
	}

	// 第 4 步之二:默认模式,落盘。
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	for _, name := range sortedKeys(files) {
		path := filepath.Join(outDir, name)
		// 内容没变就跳过。既是幂等,也是为了不动 mtime——否则每跑一次都会惊动 Vite / tsc
		// 的文件监听去重编译,编辑器还会冒出"文件已在磁盘上更改"。
		// 注意这里是**逐字节**比较(不做 EOL 归一):写盘一律 \n,生成物换行必须统一。
		if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, files[name]) {
			continue // already up to date; keep the run idempotent (zero diff, no mtime churn)
		}
		if err := os.WriteFile(path, files[name], 0o600); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		fmt.Println("protogen: wrote", path)
	}
	return nil
}

// normalizeEOL strips carriage returns so --check tolerates a CRLF checkout
// (git autocrlf) of content that is otherwise identical.
//
// 中文:只用于 --check 的比对。Windows 上 git autocrlf 会把检出的文件换成 CRLF,内容其实
// 一模一样,不归一化的话 CI 在 Windows 上会永远报"漂移"。写盘路径故意不做这层归一
// (见上),生成物始终是 \n。
func normalizeEOL(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// sortedKeys 返回 map 的键并排序。Go 的 map 遍历顺序是随机的,凡是会影响**输出内容或报错
// 顺序**的遍历都必须先排序——否则同样的输入两次运行产出不同,--check 会随机翻车。
// emit.go 里拼 import 清单等处也用它。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
