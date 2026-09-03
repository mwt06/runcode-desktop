package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"reflect"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	// 直接 import 桌面自己的 protocol 包,为的是拿 CommandKinds 那张命令分类表。
	// 注意这是**编译期依赖**:protogen 用的表和被校验的表是同一份代码,不可能版本错开
	// (换成读文件或读 AST 就有"改了源码但生成器还在用旧表"的空档)。
	deskproto "github.com/wt68/runcode/internal/protocol"
)

// extract.go 是 protogen 的「规则层」:把三个 Go 包读成一份与语言无关的中间模型 model,
// 并在这一步跑完全部一致性校验。emit.go 只负责把 model 渲染成文本、不做任何判断——
// 所以"什么能上 wire、什么必须报错"的决定**全部集中在本文件**。
//
// 文件结构(从上往下):
//
//  1. 几张手工维护的表 —— eventPayloads / excludedMethods / genericStructs /
//     fieldTypeOverrides / constGroupSpecs / constExemptTypes。它们是"人做的决定",
//     Go 类型信息里推不出来;每张表都配了一道与源码交叉核对的校验,漏改必然报错。
//  2. model 及其子类型 —— 中间表示,字段刻意做得扁平、可排序。
//  3. extract —— 主流程:声明扫描 → 常量分组 → 事件表核对 → 命令抽取。
//  4. extractXxx / typeMapper —— 各条具体规则。
//
// 一条贯穿全文的纪律:**宁可报错,不可静默降级**。生成器只要"猜"过一次,错的结果就会被
// 写进版本库,还会被 --check 当成下一次的基准,从此再没人发现。

// eventPayloads is the explicit event→payload table: each Event* constant of
// either protocol package mapped to the protocol type name of its payload.
// Adding an event constant without adding it here fails generation, so the
// EventMap can never silently miss an event (and vice versa for stale rows).
//
// 中文:为什么要手工维护而不是自动推断?因为"事件名 → 载荷类型"这层对应关系在 Go 里
// 根本不存在——事件常量只是个字符串,`app.Event.Emit(name, envelope)` 那头的 payload 是
// any,类型信息在编译期就丢了。它也不是简单的重名映射:下面 EventAssistantDelta 与
// EventAssistantThinking 共用同一个 AssistantDelta 载荷,任何"按名字猜"的规则都会猜错。
//
// 手工表的代价是会忘记改,所以 extractEvents 做**双向**核对:常量多了(漏加表行)报
// missing,表行多了(常量已删)报 stale。表本身不是真理,与源码对上的表才是。
var eventPayloads = map[string]string{
	"EventAssistantDelta":     "AssistantDelta",
	"EventAssistantThinking":  "AssistantDelta",
	"EventContextUsage":       "ContextUsage",
	"EventHarmAutoAllow":      "HarmAutoAllow",
	"EventPassportChanged":    "PassportStatus",
	"EventRecorderLevel":      "RecorderLevel",
	"EventRecorderState":      "RecorderState",
	"EventRecorderTranscript": "RecorderTranscript",
	"EventRetry":              "RetryNotice",
	"EventPermissionRequest":  "PermissionRequest",
	"EventPlanUpdated":        "PlanRun",
	"EventSessionRenamed":     "SessionRenamed",
	"EventSkillInstall":       "SkillInstallProgress",
	"EventUpdate":             "UpdateInfo",
	"EventToolEvent":          "ToolEvent",
	"EventTurnEnd":            "TurnEnd",
	"EventTurnQueued":         "TurnQueued",
	"EventTurnError":          "TurnError",
	"EventWarning":            "Warning",
}

// excludedMethods are App methods that are shell wiring, not wire commands.
//
// 中文:App 上的导出方法**默认全是前端命令**,这里是唯一的例外名单。四个都是 Wails 外壳
// 自己调的接线方法(装设备、启动、收尾),前端永远够不着,生成出来只会是四个调了必炸的
// TS 函数。
//
// 加进来的方法同时会从 methodSet 里消失,所以**必须一并从 internal/protocol.CommandKinds
// 里删掉**,否则 extractCommands 的交叉核对会把它判成 orphaned(登记了却没有对应方法)。
// 这个连带关系是故意的:排除一个命令是件需要两处都点头的事。
var excludedMethods = map[string]bool{
	"SetCapturer": true, // installs the system audio capturer; called by the Wails shell, never the frontend
	"SetDialoger": true, // installs the native file-dialog provider; called by the Wails shell, never the frontend
	"SetQuitter":  true, // installs the app-quit provider (版本更新装完要退出); called by the Wails shell, never the frontend
	"Shutdown":    true, // once-per-run teardown; called by the Wails shell from OnShutdown, never the frontend
	"Startup":     true, // once-per-run background work; called by the Wails shell from OnStartup, never the frontend
}

// genericStructs declares protocol structs emitted as generic TS interfaces,
// with per-field (json name) type overrides referencing the type parameters.
//
// 中文:Go 没有"把 any 字段参数化"的表达力,但 TS 有。Envelope 的 `Payload any` 直译成
// `payload: unknown`,前端每次订阅事件都得自己断言一次;声明成 Envelope<P> 之后,
// events.ts 里的 onEnvelope<K> 就能把 EventMap 查到的载荷类型一路带进来,订阅方零断言。
//
// 这是全文件唯一"生成物比 Go 源码类型更强"的地方,所以只能白名单、逐个手工声明。
var genericStructs = map[string]struct {
	typeParams string
	fields     map[string]string
}{
	// Envelope's `payload any` becomes the type parameter so EventMap payloads
	// flow through onEnvelope with full typing.
	"Envelope": {typeParams: "<P = unknown>", fields: map[string]string{"payload": "P"}},
}

// fieldTypeOverrides retypes struct fields (by json name) to a generated
// union instead of the raw Go type, wiring the discriminated unions in.
//
// 中文:把字段从"宽类型"收窄到"生成出来的联合类型"。ToolEvent.Type 在 Go 里就是个
// string,直译成 `type: string` 的话,前端 switch 时既没有补全也没有穷尽性检查;改写成
// ToolEventType 之后它就是判别联合的判别字段,漏一个分支 tsc 会当场报错。
//
// 为什么不自动推断(比如"字段类型是具名 string 类型就查对应联合")?因为 Go 侧这些字段
// 声明的就是裸 string——不是所有常量组都定义了具名类型,而且哪个字段配哪个联合是语义,
// 不是形状。按 json 名索引则保证与生成物里的属性名一一对应。
var fieldTypeOverrides = map[string]map[string]string{
	"ToolEvent": {"type": "ToolEventType"},
	"Error":     {"code": "ErrCode"},
	// 更新界面完全按 stage 分支渲染，收窄成联合之后漏一个分支 tsc 会当场报错——
	// 这正是「下好了但按钮还停在下载」那类缺陷的来源。
	"UpdateInfo": {"stage": "UpdateStage"},
	"PlanRun":    {"state": "PlanState", "stage": "PlanStage"},
}

// constGroupSpec describes one Go constant-prefix group emitted as a TS const
// object plus a literal-union type. open adds `| (string & {})` so unknown
// values from a newer host remain assignable (forward compatibility).
//
// 中文:每个 spec 描述"一组 Go 常量怎么变成 TS"。goPrefix 是识别用的前缀(见 matchGroup),
// constName 是生成的常量对象名,unionName 是联合类型名,两段 doc 原样写进生成物的注释里。
//
// open 是这里最需要想清楚的一位:
//   - open=true  → 联合末尾追加 `| (string & {})`,未知值仍可赋值。用于**跨版本对端**发来的
//     值(工具事件类型、错误码、审批决定):新 host 加了一种,老前端要能编译过、优雅降级。
//     `string & {}` 是 TS 的惯用法——既接受任意 string,又保留已知字面量的补全提示。
//   - open=false → 封闭联合,未知值编译不过。用于**本外壳自产自销**的值(事件名、计划阶段):
//     出现表外的值只可能是自己写错了,让它当场炸掉才对。
type constGroupSpec struct {
	goPrefix  string
	constName string
	unionName string
	open      bool
	doc       string
	unionDoc  string
}

// constGroupSpecs is the fixed emission order of the constant groups.
//
// 中文:这张表既是"哪些常量要生成",也是生成顺序(groupOrder 按下标排序,组内再按 key 排)。
// 顺序写死而不按名字排,是为了让 types.ts 里相关的几组挨在一起、diff 时稳定。
//
// 新增一组常量时必须在这里登记,否则 extract 会因为"匹配不到任何前缀"直接报错——
// 这是刻意的强制选择,见下面 ungrouped 那段。
var constGroupSpecs = []constGroupSpec{
	{
		goPrefix: "Event", constName: "Events", unionName: "EventName", open: false,
		doc:      "Event names emitted by the host; payload types are mapped in events.ts.",
		unionDoc: "EventName is the union of known event names.",
	},
	{
		goPrefix: "ToolEvent", constName: "ToolEventTypes", unionName: "ToolEventType", open: true,
		doc:      "Tool event types, the discriminator values of ToolEvent.type.",
		unionDoc: "ToolEventType keeps unknown discriminators assignable so a newer host degrades gracefully.",
	},
	{
		goPrefix: "Decision", constName: "Decisions", unionName: "Decision", open: true,
		doc:      "Decision values a client passes to resolvePermission; the host fails closed on anything else.",
		unionDoc: "Decision keeps unknown values assignable; the host treats them as deny.",
	},
	{
		goPrefix: "ErrCode", constName: "ErrCodes", unionName: "ErrCode", open: true,
		doc:      "Error codes carried by protocol.Error; clients switch on code.",
		unionDoc: "ErrCode keeps unknown codes assignable so a newer host degrades gracefully.",
	},
	{
		goPrefix: "PlanStage", constName: "PlanStages", unionName: "PlanStage", open: false,
		doc:      "Stages of 计划模式's planning pipeline, in order; the values of plan_write's stage argument.",
		unionDoc: "PlanStage is the closed set of planning stages — the shell's own pipeline, so an unknown value is a bug, not a newer peer.",
	},
	{
		goPrefix: "PlanState", constName: "PlanStates", unionName: "PlanState", open: false,
		doc:      "Lifecycle states of a planning run, carried by PlanRun.state.",
		unionDoc: "PlanState is the closed set of planning-run states.",
	},
	{
		goPrefix: "SkillInstall", constName: "SkillInstallStages", unionName: "SkillInstallStage", open: false,
		doc:      "Stages of installing a market skill, in order; the values of SkillInstallProgress.stage.",
		unionDoc: "SkillInstallStage is the closed set of install stages — the shell drives this pipeline itself, so an unknown value is a bug, not a newer peer.",
	},
	{
		goPrefix: "Update", constName: "UpdateStages", unionName: "UpdateStage", open: false,
		doc:      "Stages of the version updater; the values of UpdateInfo.stage.",
		unionDoc: "UpdateStage is the closed set of updater stages — the shell drives this state machine itself, so an unknown value is a bug, not a newer peer.",
	},
}

// constExemptTypes names constant types that are transport metadata rather than
// wire values a client switches on, so their constants generate no TS and don't
// trip the ungrouped-constant check. CommandKind classifies App methods
// server-side; the per-command kinds already reach TS through the commands table
// (which has its own missing/stale sync check).
//
// 中文:"这组常量不上 TS"的白名单,与 constGroupSpecs 构成非此即彼的二选一。
// CommandKind(query / idempotent-set / trigger)是服务端给命令分类用的,前端不 switch 它;
// 每条命令的 kind 已经以注释形式随 commands.ts 一起生成了,那条路另有自己的核对。
//
// 按**类型**豁免而不是按名字前缀,是因为前缀是弱约定(谁都能起个 CommandXxx 的名字),
// 具名类型是编译器认的强约束:给某个常量换类型是件显式的事,不会手滑。
var constExemptTypes = map[string]bool{
	"CommandKind": true,
}

// constTypeName returns the declared (named) type of a constant, "" for untyped
// or basic-typed constants.
//
// 中文:types.Unalias 先剥掉类型别名(Go 1.22 起 `type A = B` 在类型系统里是独立对象),
// 否则给豁免类型起个别名就能绕过检查。返回 "" 表示这常量是无类型的或纯基础类型
// (`const Foo = "x"`),那种没有可豁免的身份,必须老老实实进某个组。
func constTypeName(c *types.Const) string {
	if named, ok := types.Unalias(c.Type()).(*types.Named); ok {
		return named.Obj().Name()
	}
	return ""
}

// model 是 extract 与 emit 之间的中间表示:**已经不含任何 go/types 对象**,只剩字符串和
// 切片。这层隔离带来两个好处——emit.go 不用懂 Go 类型系统(它只是拼字符串),以及所有
// 顺序在进 emit 之前就固定死了(切片而不是 map),生成物天然可复现。
type model struct {
	version    int64  // protocol.Version
	versionDoc string // its Go doc synopsis
	groups     []constGroup
	structs    []structDef // sorted by name
	events     []eventDef  // sorted by wire name
	commands   []commandDef
}

type constGroup struct {
	spec  constGroupSpec
	items []constItem // sorted by key
}

type constItem struct{ key, value string }

// structDef 对应生成物里的一个 TS interface。
// 注意 fields 保持 **Go 声明顺序**(不排序)——结构体字段的排列是作者刻意组织的信息,
// 排序反而更难读;而 model.structs 之间按名排序,是因为两个包合并后需要一个稳定次序。
// 一句话:同一个类型内部忠于源码,跨类型之间忠于确定性。
type structDef struct {
	name       string
	doc        string
	typeParams string
	fields     []fieldDef // Go declaration order
}

type fieldDef struct {
	name     string
	optional bool
	tsType   string
}

type eventDef struct {
	wire    string // event name on the wire, e.g. "assistant:delta"
	payload string // TS interface name
}

type commandDef struct {
	goName string
	tsName string
	kind   string
	doc    string
	params []paramDef
	result string // TS type inside Promise<...>; "void" for none
}

type paramDef struct {
	name   string
	tsType string
}

// extract builds the emit model from the wire-contract packages plus the App.
// The protocol packages are read as one contract: their declarations are merged
// and re-sorted by name, so which of the two a type lives in never shows up in
// the generated TypeScript. A name declared in both is a conflict, not a merge.
func extract(protoPkgs []*packages.Package, deskPkg *packages.Package) (*model, error) {
	m := &model{}
	// docs:名字 → 文档首句,原样抄进生成物的注释。它来自 AST(见 collectDocs),
	// 因为 go/types 的类型信息里根本不含注释。两个包的 docs 合并成一张表——合法,
	// 因为下面的 checkNoDuplicateNames 已经保证了名字不会撞。
	docs := map[string]string{}
	// mapper.protocolPaths 是"哪些包的类型算 wire 类型"的身份集合。整个防腐层就靠它:
	// 签名上出现集合外的类型 = 引擎内部类型漏到了 wire 上,直接判失败。
	mapper := &typeMapper{protocolPaths: map[string]bool{}}
	for _, p := range protoPkgs {
		mapper.protocolPaths[p.PkgPath] = true
		for name, doc := range collectDocs(p) {
			docs[name] = doc
		}
	}
	// 重名检查放在最前面:两个包要合成同一个 TS 模块,重名会让后声明的悄悄顶掉先声明的。
	// 后面所有逻辑都建立在"名字唯一"这个前提上(docs 表、structNames 表都按名字索引),
	// 所以这道校验必须先过。
	if err := checkNoDuplicateNames(protoPkgs); err != nil {
		return nil, err
	}

	structNames := map[string]bool{}
	consts := map[string]*types.Const{}
	var constNames []string
	// 扫描两个包的**包级作用域**:scope.Names() 给的是包里所有顶层声明的名字(已排序),
	// Lookup 拿到的是带完整类型信息的对象。这就是"用编译器读代码"而不是文本匹配——
	// 结构体、常量、别名、是否导出,统统是问出来的,不是猜出来的。
	for _, p := range protoPkgs {
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			switch obj := scope.Lookup(name).(type) {
			case *types.TypeName:
				// 未导出的类型上不了 wire;类型别名(`type A = B`)会指向同一个底层类型,
				// 生成两份内容相同的 interface 只会互相打架,所以只认真身。
				if !obj.Exported() || obj.IsAlias() {
					continue
				}
				st, ok := obj.Type().Underlying().(*types.Struct)
				if !ok {
					continue // non-struct named types (e.g. CommandKind) have no wire shape
					// 中文:具名的非结构体(如 `type CommandKind string`)没有"形状"可言,
					// 它们的意义在常量取值上,由下面的常量分组负责,不生成 interface。
				}
				def, err := extractStruct(name, st, docs[name], mapper)
				if err != nil {
					return nil, err
				}
				m.structs = append(m.structs, def)
				structNames[name] = true
			case *types.Const:
				if !obj.Exported() {
					continue
				}
				consts[name] = obj
				constNames = append(constNames, name)
			}
		}
	}
	// 合并后重新排序。单个包的 scope.Names() 本来就是有序的,但两个包拼在一起就不是了;
	// 按名字重排之后,"某个类型属于哪个包"在生成物里彻底看不出来——这正是包头说的
	// "两个包当成一份契约读"的落点。
	sort.Slice(m.structs, func(i, j int) bool { return m.structs[i].name < m.structs[j].name })
	sort.Strings(constNames)

	// Constants → const groups (+ protocol.Version).
	var eventConsts []string
	var ungrouped []string
	for _, name := range constNames {
		c := consts[name]
		// Version 是唯一一个特事特办的常量:协议版本号,整数,单独生成 ProtocolVersion。
		// 它不属于任何"组",按名字硬认即可。
		if name == "Version" {
			v, ok := constant.Int64Val(c.Val())
			if !ok {
				return nil, fmt.Errorf("protocol.Version is not an integer constant")
			}
			m.version = v
			m.versionDoc = docs[name]
			continue
		}
		if c.Val().Kind() != constant.String {
			continue // e.g. future non-string constants; nothing to mirror
		}
		grp := matchGroup(name)
		if grp == nil {
			// No silent drops: a string constant that matches no group prefix is
			// either transport metadata (its type is exempted below) or a mistake —
			// someone added a constant group and forgot to register it, and the TS
			// side would just silently miss it. Force the decision.
			if !constExemptTypes[constTypeName(c)] {
				ungrouped = append(ungrouped, name)
			}
			continue
		}
		// TS 侧的键是去掉前缀后的部分:EventTurnEnd → Events.TurnEnd。
		// 值是常量的实际字符串(如 "turn:end"),wire 上跑的是它,而不是 Go 常量名。
		item := constItem{key: strings.TrimPrefix(name, grp.goPrefix), value: constant.StringVal(c.Val())}
		for i := range m.groups {
			if m.groups[i].spec.goPrefix == grp.goPrefix {
				m.groups[i].items = append(m.groups[i].items, item)
				grp = nil // 借 grp 当"已归入现有组"的标记,免得再开一个 bool
				break
			}
		}
		if grp != nil {
			m.groups = append(m.groups, constGroup{spec: *grp, items: []constItem{item}})
		}
		// 顺手挑出事件常量给下面的事件表核对用。这里可以用朴素的 HasPrefix,因为
		// ToolEventXxx 并不以 "Event" 开头——真正会撞的是 matchGroup 那边,它按最长前缀
		// 匹配,所以 ToolEvent* 归 ToolEvent 组而不是 Event 组。
		if strings.HasPrefix(name, "Event") {
			eventConsts = append(eventConsts, name)
		}
	}
	if len(ungrouped) > 0 {
		sort.Strings(ungrouped)
		return nil, fmt.Errorf("exported string constants in protocol match no const group prefix and no exempt type — register a constGroupSpec (they become a TS union) or add their type to constExemptTypes (transport metadata):\n  %s",
			strings.Join(ungrouped, ", "))
	}
	// Fixed group order, sorted items.
	sort.Slice(m.groups, func(i, j int) bool {
		return groupOrder(m.groups[i].spec.goPrefix) < groupOrder(m.groups[j].spec.goPrefix)
	})
	for i := range m.groups {
		sort.Slice(m.groups[i].items, func(a, b int) bool {
			return m.groups[i].items[a].key < m.groups[i].items[b].key
		})
	}

	// Events: the explicit table must exactly cover the Event* constants.
	events, err := extractEvents(consts, eventConsts, structNames)
	if err != nil {
		return nil, err
	}
	m.events = events

	// Commands from desktop.App.
	cmds, err := extractCommands(deskPkg, mapper)
	if err != nil {
		return nil, err
	}
	m.commands = cmds
	return m, nil
}

// checkNoDuplicateNames rejects an exported name declared by more than one
// protocol package. The two are emitted into a single TypeScript module, so a
// collision would silently drop one declaration; it also means a type was moved
// between the packages without deleting the original.
//
// 中文:两个包合成一个 TS 模块,同名声明只会剩下一个——静默丢掉的那半边,前端却仍以为
// 自己拿到的是它。这道检查还顺带抓一类真实事故:把类型从引擎包挪到本仓时忘了删原处,
// 两边各留一份、字段还渐渐长歪。
//
// 检查的是**所有导出名**(类型、常量、函数一视同仁),不只是会生成 TS 的那些——重名本身
// 就说明两个包的边界没划清楚,该在这里就吵起来,而不是等生成物出问题。
func checkNoDuplicateNames(protoPkgs []*packages.Package) error {
	owner := map[string]string{}
	var dups []string
	for _, p := range protoPkgs {
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			if obj := scope.Lookup(name); obj == nil || !obj.Exported() {
				continue
			}
			if first, seen := owner[name]; seen {
				dups = append(dups, fmt.Sprintf("%s (in %s and %s)", name, first, p.PkgPath))
				continue
			}
			owner[name] = p.PkgPath
		}
	}
	if len(dups) > 0 {
		sort.Strings(dups)
		return fmt.Errorf("the protocol packages declare the same exported name twice; they are emitted as one TypeScript module, so each name may only be defined once:\n  %s",
			strings.Join(dups, "\n  "))
	}
	return nil
}

// groupOrder 把前缀翻译成 constGroupSpecs 里的下标,用作生成顺序;没登记的排到最后
// (实际到不了这一步,ungrouped 校验已经拦下了)。线性扫描——六个元素,不值得上 map。
func groupOrder(prefix string) int {
	for i, g := range constGroupSpecs {
		if g.goPrefix == prefix {
			return i
		}
	}
	return len(constGroupSpecs)
}

// matchGroup finds the const group for a constant name, longest prefix first
// so ToolEvent* is never mistaken for Event*.
//
// 中文:前缀集合天生会互相包含。现成的例子就是 PlanStage* 与 PlanState*——真要再加个
// "Plan" 组,一个 PlanStageDraft 就会同时匹配 Plan 和 PlanStage 两条 spec。
// 取**最长**前缀是唯一稳妥的裁决:它只取决于前缀本身,与 constGroupSpecs 的排列无关,
// 所以调整表的顺序(那是生成顺序)永远不会悄悄改变某个常量的归属。
func matchGroup(name string) *constGroupSpec {
	var best *constGroupSpec
	for i := range constGroupSpecs {
		g := &constGroupSpecs[i]
		if strings.HasPrefix(name, g.goPrefix) && (best == nil || len(g.goPrefix) > len(best.goPrefix)) {
			best = g
		}
	}
	return best
}

// extractStruct 把一个 Go 结构体翻成 TS interface 的定义。
// 核心是"按 JSON 的规则读结构体",而不是按 Go 的规则:字段名取 json tag、可选性取
// omitempty、`,string` 会改变线上类型——生成物要描述的是**序列化之后的形状**。
func extractStruct(name string, st *types.Struct, doc string, mapper *typeMapper) (structDef, error) {
	def := structDef{name: name, doc: doc}
	if g, ok := genericStructs[name]; ok {
		def.typeParams = g.typeParams
	}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if !f.Exported() {
			continue // unexported fields never serialize
		}
		// 内嵌字段在 JSON 里会**提升**(字段平铺到外层),要正确生成就得递归展开、
		// 还要处理同名覆盖与 tag 冲突。wire 类型本来就该是扁平 DTO,与其实现一套容易出错的
		// 提升规则,不如直接不支持、让作者把字段写明白。
		if f.Embedded() {
			return def, fmt.Errorf("protocol.%s embeds %s: embedded fields are not supported by protogen", name, f.Name())
		}
		jsonName, optional, stringEncoded, skip := parseJSONTag(reflect.StructTag(st.Tag(i)).Get("json"), f.Name())
		if skip {
			continue
		}
		tsType, err := fieldTSType(name, jsonName, f.Type(), optional, mapper)
		if err != nil {
			return def, fmt.Errorf("protocol.%s.%s: %w", name, f.Name(), err)
		}
		if stringEncoded && isStringEncodable(f.Type()) {
			// `,string` wraps the value in a JSON string on the wire (typically to
			// keep 64-bit integers exact past JS float precision) — the Go type no
			// longer reflects the wire type.
			tsType = "string"
		}
		def.fields = append(def.fields, fieldDef{name: jsonName, optional: optional, tsType: tsType})
	}
	return def, nil
}

// fieldTSType maps one struct field's Go type to TS, applying the generic and
// union overrides and the omitempty-pointer rule (`f?: T` rather than `f?: T | null`,
// since a nil pointer with omitempty is omitted, never null).
//
// 中文:三层优先级,自上而下——泛型参数覆盖 > 联合类型覆盖 > 按 Go 类型直译。
//
// 最后那条 omitempty + 指针的规则值得单独记:`*T` + omitempty 时,nil 会被**整个省略**
// 而不是写成 null,所以正确的 TS 是 `f?: T`;要是写成 `f?: T | null`,前端就得白白处理一种
// 永远不会出现的取值。反过来,不带 omitempty 的 `*T` 里 nil 确实序列化成 null,那时才该带
// `| null`(交给 mapper.ts 的 nullable=true 处理)。
func fieldTSType(structName, jsonName string, t types.Type, optional bool, mapper *typeMapper) (string, error) {
	if g, ok := genericStructs[structName]; ok {
		if over, ok := g.fields[jsonName]; ok {
			return over, nil
		}
	}
	if over, ok := fieldTypeOverrides[structName][jsonName]; ok {
		return over, nil
	}
	if ptr, ok := types.Unalias(t).(*types.Pointer); ok && optional {
		return mapper.ts(ptr.Elem(), true)
	}
	return mapper.ts(t, true)
}

// parseJSONTag returns the wire name, whether omitempty is set, whether the
// value is string-encoded on the wire (`,string`), and whether the field is
// excluded from JSON entirely. `json:"-"` skips; `json:"-,"` is encoding/json's
// escape for a field literally named "-".
//
// 中文:这是一段"复刻 encoding/json 行为"的代码,不是自创规则——生成物必须与运行时实际
// 序列化出来的 JSON 一致,所以 tag 的每条语义都得照抄:
//   - 无 tag / tag 里没写名字 → 用 Go 字段名(注意 encoding/json **不会**自动转小写)
//   - `json:"-"` → 完全不序列化,跳过
//   - `json:"-,"` → 例外中的例外:字段名就叫 "-",所以只有"单独一个减号"才算跳过
//   - `omitempty` → 零值时省略,对应 TS 的可选属性 `f?:`
//   - `,string` → 值在线上被包成字符串,见 isStringEncodable
func parseJSONTag(tag, goName string) (name string, optional, stringEncoded, skip bool) {
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "-" && len(parts) == 1 {
		return "", false, false, true
	}
	if name == "" {
		name = goName
	}
	for _, opt := range parts[1:] {
		switch opt {
		case "omitempty":
			optional = true
		case "string":
			stringEncoded = true
		}
	}
	return name, optional, stringEncoded, false
}

// isStringEncodable mirrors the types encoding/json honors `,string` for —
// string, bool, integer, and floating-point (plus pointers to them); the option
// is ignored elsewhere, so the TS override must not fire there either.
//
// 中文:`,string` 只对 string/bool/整数/浮点(及其指针)生效,写在切片、结构体上会被
// encoding/json **静默忽略**。所以这里必须同样地忽略——否则给一个 []T 字段随手写了
// `,string`,生成物就会声称它是 string,而运行时发来的其实是数组,前端拿到的类型是假的。
//
// 复刻标准库的怪脾气,好过发明一套"更合理"的规则:线上跑的是标准库,不是我们。
func isStringEncodable(t types.Type) bool {
	t = types.Unalias(t)
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return b.Info()&(types.IsString|types.IsBoolean|types.IsInteger|types.IsFloat) != 0
}

// extractEvents 把 Event* 常量与手工的 eventPayloads 表对齐成事件定义清单。
//
// 三道检查一道都不能少:
//   - missing:有常量没表行 → 新加了事件却忘了声明载荷类型,EventMap 会漏掉它
//   - stale:有表行没常量 → 事件删了表没删,生成的 EventMap 会引用不存在的事件名
//   - 载荷类型不存在 → 表里写了个拼错的类型名,生成的 TS 会 import 一个没有的 interface
//
// 前两道**必须双向**做:只查一边的话,漏掉的那一边永远无人发现。
func extractEvents(consts map[string]*types.Const, eventConsts []string, structNames map[string]bool) ([]eventDef, error) {
	constSet := map[string]bool{}
	for _, n := range eventConsts {
		constSet[n] = true
	}
	var missing, stale []string
	for _, n := range eventConsts {
		if _, ok := eventPayloads[n]; !ok {
			missing = append(missing, n)
		}
	}
	for n := range eventPayloads {
		if !constSet[n] {
			stale = append(stale, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 || len(stale) > 0 {
		var b strings.Builder
		b.WriteString("event table (eventPayloads in tools/protogen) is out of sync with the protocol package's Event* constants:")
		if len(missing) > 0 {
			fmt.Fprintf(&b, "\n  missing table rows for: %s", strings.Join(missing, ", "))
		}
		if len(stale) > 0 {
			fmt.Fprintf(&b, "\n  stale table rows (no such constant): %s", strings.Join(stale, ", "))
		}
		return nil, fmt.Errorf("%s", b.String())
	}

	events := make([]eventDef, 0, len(eventConsts))
	for _, n := range eventConsts {
		payload := eventPayloads[n]
		// structNames 是上面扫描时攒下的"确实生成了 interface 的类型名"集合。
		// 表里写错一个字母,在这里就会被逮住,而不是等 tsc 报 import 不存在。
		if !structNames[payload] {
			return nil, fmt.Errorf("event table maps %s to unknown protocol type %s", n, payload)
		}
		events = append(events, eventDef{wire: constant.StringVal(consts[n].Val()), payload: payload})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].wire < events[j].wire })
	return events, nil
}

// extractCommands 把 desktop.App 的导出方法抽成命令清单。
//
// 这是三份生成物里唯一不来自 protocol 包的部分:命令面的事实源是**方法签名本身**,
// 而不是任何一张手写的清单——想加一条前端命令,给 App 加个导出方法即可,TS 侧自动出现。
// 代价是必须把"哪些方法不算命令""每条命令是什么语义"这两件签名表达不了的事补上,
// 前者靠 excludedMethods,后者靠 internal/protocol.CommandKinds,两者都做交叉核对。
func extractCommands(deskPkg *packages.Package, mapper *typeMapper) ([]commandDef, error) {
	// 按名字从包作用域里捞 App 类型。这里的字符串 "App" 与 emit.go 里的 appServiceFQN
	// 是同一件事的两半:Go 侧找方法用它,TS 侧拼 Wails 绑定名也用它。
	obj := deskPkg.Types.Scope().Lookup("App")
	if obj == nil {
		return nil, fmt.Errorf("type App not found in %s", deskPkg.PkgPath)
	}
	named, ok := obj.Type().(*types.Named)
	if !ok {
		return nil, fmt.Errorf("%s.App is not a named type", deskPkg.PkgPath)
	}
	methodDocs := collectMethodDocs(deskPkg, "App")

	var cmds []commandDef
	var violations []string
	methodSet := map[string]bool{}
	for i := 0; i < named.NumMethods(); i++ {
		fn := named.Method(i)
		if !fn.Exported() || excludedMethods[fn.Name()] {
			continue
		}
		methodSet[fn.Name()] = true
		cmd, errs := extractCommand(fn, methodDocs[fn.Name()], mapper)
		if len(errs) > 0 {
			// 收集而不是立刻返回:一次跑完能把所有越界签名全报出来,免得改一个发现一个。
			violations = append(violations, errs...)
			continue
		}
		cmds = append(cmds, cmd)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		return nil, fmt.Errorf("app methods with signatures that cannot cross the wire (only protocol and basic types may appear):\n  %s",
			strings.Join(violations, "\n  "))
	}

	// Cross-check against the shell's own internal/protocol.CommandKinds (imported directly, so it can
	// never skew from the sources protogen was built against).
	//
	// 中文:这就是 docs/system-overview.md 里说的"编译器管不到的那条边,由代码生成器管"。
	// CommandKinds 是一张给命令分语义(query / idempotent-set / trigger)的表,Go 编译器不会
	// 检查它是否覆盖了 App 的每个方法——漏登记不报错,登记了不存在的方法也不报错。
	// 于是在这里双向核对:方法没登记 → unclassified,登记了没方法 → orphaned,任一非空即失败。
	var unclassified, orphaned []string
	for name := range methodSet {
		if _, ok := deskproto.CommandKinds[name]; !ok {
			unclassified = append(unclassified, name)
		}
	}
	for name := range deskproto.CommandKinds {
		if !methodSet[name] {
			orphaned = append(orphaned, name)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(orphaned)
	if len(unclassified) > 0 || len(orphaned) > 0 {
		var b strings.Builder
		b.WriteString("internal/protocol.CommandKinds is out of sync with desktop.App's exported methods:")
		if len(unclassified) > 0 {
			fmt.Fprintf(&b, "\n  methods missing a CommandKinds entry: %s", strings.Join(unclassified, ", "))
		}
		if len(orphaned) > 0 {
			fmt.Fprintf(&b, "\n  CommandKinds entries with no App method: %s", strings.Join(orphaned, ", "))
		}
		return nil, fmt.Errorf("%s", b.String())
	}

	// 核对通过后才回填 kind——保证每次取值都命中,不会写出空字符串。
	for i := range cmds {
		cmds[i].kind = string(deskproto.CommandKinds[cmds[i].goName])
	}
	// 按 TS 函数名排序:生成物的阅读顺序应当跟着生成物走,而不是跟着 go/types 给方法的
	// 内部顺序走(那个顺序在方法分布到不同文件时并不直观)。
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].tsName < cmds[j].tsName })
	return cmds, nil
}

// extractCommand maps one App method to a command definition. It returns the
// violations (never a partial success) when the signature leaks non-wire types.
//
// 中文:一条命令的形状受三条约束,都是 Wails/JSON 这条通道决定的——
//   - 不支持可变参数:JS 侧的调用是按位置传参的固定列表,变参没有对应表达
//   - 返回值只能是 ()、(error)、(T) 或 (T, error):error 在 TS 里体现为 Promise 拒绝,
//     不占返回类型;超出这四种形态说明作者想表达 protogen 没约定过的东西,该停下来讨论
//   - 参数与返回值的类型必须全在 protocol 包内(或基础类型),否则就是引擎内部类型漏上 wire
func extractCommand(fn *types.Func, doc string, mapper *typeMapper) (commandDef, []string) {
	sig := fn.Type().(*types.Signature)
	cmd := commandDef{goName: fn.Name(), tsName: lowerCamel(fn.Name()), doc: doc}
	var violations []string

	if sig.Variadic() {
		violations = append(violations, fmt.Sprintf("%s: variadic parameters are not supported", fn.Name()))
	}
	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		p := params.At(i)
		name := p.Name()
		if name == "" || name == "_" {
			name = fmt.Sprintf("arg%d", i)
		}
		// Parameters flow client→host: the caller always passes a value, so
		// slices/maps are non-nullable here (unlike results, where a Go nil
		// serializes to null).
		tsType, err := mapper.ts(p.Type(), false)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s: parameter %s: %v", fn.Name(), name, err))
			continue
		}
		cmd.params = append(cmd.params, paramDef{name: sanitizeIdent(name), tsType: tsType})
	}

	results := sig.Results()
	switch results.Len() {
	case 0:
		cmd.result = "void"
	case 1:
		if isErrorType(results.At(0).Type()) {
			cmd.result = "void"
		} else {
			ts, err := mapper.ts(results.At(0).Type(), true)
			if err != nil {
				violations = append(violations, fmt.Sprintf("%s: result: %v", fn.Name(), err))
			}
			cmd.result = ts
		}
	case 2:
		if !isErrorType(results.At(1).Type()) {
			violations = append(violations, fmt.Sprintf("%s: second result must be error, got %s", fn.Name(), results.At(1).Type()))
			break
		}
		ts, err := mapper.ts(results.At(0).Type(), true)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s: result: %v", fn.Name(), err))
		}
		cmd.result = ts
	default:
		violations = append(violations, fmt.Sprintf("%s: %d results — multi-value returns beyond (T, error) need a protogen decision", fn.Name(), results.Len()))
	}
	return cmd, violations
}

// isErrorType 判断一个类型是不是内建的 error。
// 判据是 `Pkg() == nil`——**universe 作用域**(内建类型所在的地方)里的对象不属于任何包,
// 这比按名字比对靠谱:自己定义一个叫 error 的类型,它的 Pkg() 就不是 nil,骗不过去。
func isErrorType(t types.Type) bool {
	named, ok := types.Unalias(t).(*types.Named)
	return ok && named.Obj().Pkg() == nil && named.Obj().Name() == "error"
}

// typeMapper converts Go wire types to TypeScript. nullable controls whether
// slices/maps/pointers carry `| null` (true for host→client values, where Go
// nil serializes as JSON null).
type typeMapper struct {
	// protocolPaths is the set of packages whose types are wire types. Anything
	// outside it on a wire signature is a leak and fails generation.
	protocolPaths map[string]bool
}

// ts 是整个生成器的类型翻译核心:一个 Go 类型 → 一段 TS 类型文本。
//
// 它是递归的(切片套 map 套指针都能展开),而且**默认拒绝**:switch 覆盖不到的形态一律
// 报错,不会退化成 any/unknown。这条"白名单式"的设计正是防腐层能成立的原因——
// 引擎内部类型只要出现在 wire 签名上,这里必然失败,而不是生成一个骗人的类型。
//
// nullable 表示"Go 的 nil 在这个方向上会不会变成 JSON null":
// host→client 的返回值与结构体字段传 true(nil slice 序列化成 null),
// client→host 的参数传 false(调用方总是给值,写 `| null` 只会逼前端多做无谓的判空)。
func (m *typeMapper) ts(t types.Type, nullable bool) (string, error) {
	// types.Unalias:先剥掉类型别名,拿到真身再判断形态。
	switch tt := types.Unalias(t).(type) {
	case *types.Named:
		obj := tt.Obj()
		if obj.Pkg() == nil {
			// universe 作用域:error、any 之类的内建类型,不是 wire 类型。
			return "", fmt.Errorf("universe type %s is not a wire type", obj.Name())
		}
		// 关键的一道门:这个类型的**包**在不在 protocol 白名单里。
		if m.protocolPaths[obj.Pkg().Path()] {
			if _, ok := tt.Underlying().(*types.Struct); ok {
				return obj.Name(), nil
			}
			// Named non-struct protocol types (string enums etc.) map by shape.
			return m.ts(tt.Underlying(), nullable)
		}
		// 唯一的外部类型特例:json.RawMessage 就是"一段未解析的 JSON",语义上等于
		// TS 的 unknown。它常用于 MCP 工具入参这类形状不定的载荷。
		if obj.Pkg().Path() == "encoding/json" && obj.Name() == "RawMessage" {
			return "unknown", nil // opaque JSON value
		}
		// 走到这里就是漏网的引擎内部类型 / 第三方类型,报错并把完整类型名打出来。
		return "", fmt.Errorf("non-protocol type %s", types.TypeString(tt, nil))
	case *types.Basic:
		// 用 Info() 的位判断而不是逐个列举 Kind:int/int8/…/uint64/float32/float64 十几种
		// 在 JS 里都是 number,按类别归并省得漏。
		// 注意:int64 在这里也变成 number,超过 2^53 会丢精度——需要精确时在 Go 侧给字段
		// 加 `,string`(见 isStringEncodable),让它以字符串上线。
		switch {
		case tt.Info()&types.IsBoolean != 0:
			return "boolean", nil
		case tt.Info()&types.IsString != 0:
			return "string", nil
		case tt.Info()&types.IsInteger != 0, tt.Info()&types.IsFloat != 0:
			return "number", nil
		default:
			return "", fmt.Errorf("unsupported basic type %s", tt)
		}
	case *types.Slice:
		if elem, ok := types.Unalias(tt.Elem()).(*types.Basic); ok && elem.Kind() == types.Uint8 {
			return "string", nil // []byte serializes as base64
		}
		inner, err := m.ts(tt.Elem(), nullable)
		if err != nil {
			return "", err
		}
		// 元素类型自己带了联合(如 `T | null`)时必须加括号:`T | null[]` 的含义是
		// "T 或 null 的数组",与想表达的 `(T | null)[]` 完全不同。
		if strings.ContainsAny(inner, " |") {
			inner = "(" + inner + ")"
		}
		if nullable {
			return inner + "[] | null", nil
		}
		return inner + "[]", nil
	case *types.Map:
		// JSON 对象的键只能是字符串。Go 允许 map[int]T(encoding/json 会把键转成字符串),
		// 但那层隐式转换在 TS 侧极易踩坑,不如直接不支持。
		if key, ok := types.Unalias(tt.Key()).(*types.Basic); !ok || key.Info()&types.IsString == 0 {
			return "", fmt.Errorf("map key %s is not a string", tt.Key())
		}
		val, err := m.ts(tt.Elem(), nullable)
		if err != nil {
			return "", err
		}
		if nullable {
			return "Record<string, " + val + "> | null", nil
		}
		return "Record<string, " + val + ">", nil
	case *types.Pointer:
		inner, err := m.ts(tt.Elem(), nullable)
		if err != nil {
			return "", err
		}
		if nullable {
			return inner + " | null", nil
		}
		return inner, nil
	case *types.Interface:
		// `any`(空接口)= 任意 JSON 值 = unknown。用 unknown 而不是 any,是让前端被迫
		// 先收窄再用,别把"不知道是什么"当成"什么都行"。
		if tt.Empty() {
			return "unknown", nil
		}
		// 非空接口意味着方法集,JSON 里没有对应物——通常是不小心把某个内部抽象放上了 wire。
		return "", fmt.Errorf("non-empty interface %s is not a wire type", tt)
	default:
		// 兜底拒绝:chan、func、struct 字面量、数组等等。宁可报错也不猜。
		return "", fmt.Errorf("unsupported type %s", types.TypeString(t, nil))
	}
}

// collectDocs maps every type/const name declared in pkg to its doc synopsis.
//
// 中文:注释只存在于 **AST** 里,go/types 的类型信息不含注释——这正是 main.go 的 Mode
// 要同时开 NeedSyntax 的原因。走一遍 pkg.Syntax(每个 .go 文件一棵语法树),把类型与常量
// 的文档首句收进表里,生成的 TS 才能带上与 Go 侧同源的说明。
//
// ValueSpec 那支有个 Go 语法上的细节:`const ( // 文档\n Foo = "x" )` 这种带括号的声明,
// 文档挂在 ValueSpec 上;而 `// 文档\nconst Foo = "x"` 不带括号时,文档挂在外层 GenDecl 上。
// 所以要先取 spec.Doc,取不到再回落到 gd.Doc(且仅限只有一个 spec 时,否则一组常量会
// 集体套上同一句说明)。
func collectDocs(pkg *packages.Package) map[string]string {
	docs := map[string]string{}
	for _, f := range pkg.Syntax {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					d := s.Doc
					if d == nil {
						d = gd.Doc
					}
					docs[s.Name.Name] = synopsis(d.Text())
				case *ast.ValueSpec:
					d := s.Doc
					if d == nil && len(gd.Specs) == 1 {
						d = gd.Doc // unparenthesized decl: the doc sits on the GenDecl
					}
					if d == nil {
						continue
					}
					for _, n := range s.Names {
						docs[n.Name] = synopsis(d.Text())
					}
				}
			}
		}
	}
	return docs
}

// collectMethodDocs maps recvType's method names to their doc synopses.
//
// 中文:同理,方法注释也得从 AST 取。这里要认的是接收者类型名,所以先剥一层 StarExpr——
// `func (a *App) Foo()` 的接收者类型是 *App,得脱掉星号才能与 "App" 比对。
// 这些注释最终会变成 commands.ts 里每个函数上方的说明,前端 hover 时看到的就是它。
func collectMethodDocs(pkg *packages.Package, recvType string) map[string]string {
	docs := map[string]string{}
	for _, f := range pkg.Syntax {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) != 1 || fd.Doc == nil {
				continue
			}
			t := fd.Recv.List[0].Type
			if star, ok := t.(*ast.StarExpr); ok {
				t = star.X
			}
			if id, ok := t.(*ast.Ident); ok && id.Name == recvType {
				docs[fd.Name.Name] = synopsis(fd.Doc.Text())
			}
		}
	}
	return docs
}

// synopsis returns the doc text's first sentence, collapsed to one line.
//
// 中文:只取首句、压成一行。生成物里的注释是单行 `// …`,原样搬多行文档会把换行带进去、
// 直接破坏生成的 TS。判据是"句号后面跟空格或到头",所以 "v1.2" 这种句中小数点不会误切。
func synopsis(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	for i := 0; i < len(s); i++ {
		if s[i] == '.' && (i+1 == len(s) || s[i+1] == ' ') {
			return s[:i+1]
		}
	}
	return s
}

// lowerCamel 把 Go 的导出方法名转成 TS 的函数名(StartSession → startSession)。
// 只动首字母:硬转成 sTARTSession 之类的边界情况不存在,因为 Go 方法名一律是大驼峰。
// 生成的 TS 函数名与 Go 方法名的对应关系必须是可逆的——commands.ts 里 call() 传的仍是
// **Go 原名**,Wails 绑定表按那个名字定位方法。
func lowerCamel(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// tsReserved are the TypeScript/JavaScript reserved words that cannot be
// parameter names; sanitizeIdent appends an underscore to escape them.
var tsReserved = map[string]bool{
	"await": true, "break": true, "case": true, "catch": true, "class": true,
	"const": true, "continue": true, "debugger": true, "default": true,
	"delete": true, "do": true, "else": true, "enum": true, "export": true,
	"extends": true, "false": true, "finally": true, "for": true,
	"function": true, "if": true, "implements": true, "import": true,
	"in": true, "instanceof": true, "interface": true, "let": true,
	"new": true, "null": true, "package": true, "private": true,
	"protected": true, "public": true, "return": true, "static": true,
	"super": true, "switch": true, "this": true, "throw": true, "true": true,
	"try": true, "typeof": true, "var": true, "void": true, "while": true,
	"with": true, "yield": true,
}

// sanitizeIdent 给撞上 TS 保留字的参数名加下划线。
// Go 里 `func (a *App) Foo(new string)` 完全合法,直译过去 `function foo(new: string)`
// 是语法错误。只改参数名是安全的——它纯属生成物内部的局部变量名,不参与任何契约
// (调用是按位置传参的,名字不上 wire)。
func sanitizeIdent(name string) string {
	if tsReserved[name] {
		return name + "_"
	}
	return name
}
