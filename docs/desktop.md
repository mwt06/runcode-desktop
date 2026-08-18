# runcode 桌面版（XRUN）架构

本文档从当前代码整理，描述桌面版的分层结构、命令/事件协议、权限审批流和构建方式。产品名为 **XRUN**（窗口标题、`wails.json`、侧栏、输出二进制均用此名），目录与模块路径仍是 `runcode`。

桌面版是引擎的第三个前端（CLI、TUI 之后），分两层：

```text
cmd/runcode-desktop/            嵌套 Go module：Wails v2 外壳 + React 前端（Wails/CGO 重依赖隔离在此）
  main.go                       事件桥、原生对话框、窗口配置、Bind(app)、//go:embed frontend/dist
  wails.json                    应用名/输出名 XRUN
  frontend/                     React 18 + Vite 6 + TS + Tailwind v4

internal/desktop/               根模块：传输无关的桌面核心（不依赖 Wails，可单测）
  app.go                        App：单会话管理器，异步跑 turn，事件驱动
  approver.go                   异步权限审批器（实现 permissions.Approver）
  config.go                     StartSessionRequest -> engine.Config 映射
  sessions.go / attachments.go  会话列表/恢复/切换工作区、图片附件
  skills.go / agents.go / mcp.go  Skills、Sub-agents、MCP servers 的 CRUD
  context.go / files.go / store.go 项目指令与记忆读写、# 文件选择器、desktop.json 持久化
  harm.go                       harm judge 接线与 auto-allow 审计事件

../agentloop（外部 module）    共享引擎 gitlab.ouc-online.com.cn/aibase/agentloop（同级 checkout，见 docs/architecture.md）
```

## internal/desktop：传输无关核心

`App`（`internal/desktop/app.go`）是引擎 `host.Manager` 之上的**单会话适配层**：会话表/事件信封与 seq/审批路由由 host 层负责（`host.NewManager(host.Options{Build: host.DefaultBuild, ...})`，单用户外壳不设配额、不做空闲回收），App 只叠加"单会话策略"——`currentID` 把所有命令路由到活动会话，`startMu` 串行化开/关/切换会话的生命周期。turn 在 host 的 goroutine 上运行、结果经事件回报，命令方法不阻塞 UI 线程。构造方式为 `New(sink)` + `SetDialoger(d)`（原生对话框由外壳注入）。

### 命令面（全部绑定给前端）

| 分组 | 方法 |
|------|------|
| 生命周期/turn | `StartSession`、`SendMessage`、`SendMessageWithImages`、`Interrupt`、`Reset`、`Status`、`CloseSession` |
| 运行时开关 | `SetPermissionMode`、`SetModel`、`SetPlanMode`、`SetReasoningScenario`、`SetThinkingEffort`、`Compact`、`SaveSettings` |
| 权限 | `ResolvePermission(id, decision)` |
| 会话/工作区 | `ListSessions`、`ResumeSession`、`NewSession`、`SwitchWorkspace`、`PickWorkspaceFolder`、`ListTools` |
| 附件/文件 | `PickImageAttachment`、`ListFiles`（`#` 选择器，上限 4000，跳过 `.git`/`.runcode`/`node_modules` 等） |
| Skills/Agents/MCP | `ListSkills`/`SaveSkill`/`DeleteSkill`/`ImportSkill`、`ListAgents`/…（内置只读）、`ListMCPServers`/`SaveMCPServer`/`DeleteMCPServer`/`SetMCPServerEnabled` |
| 上下文/配置 | `ReadProjectContext`、`SaveProjectContext`、`ReadMemory`、`LoadConfig` |
| 阶段化计划模式 | `PlanStatus`、`PlanUpdate(doc)`、`PlanApprove(doc, permissionMode)`、`PlanCancel` |

Skills/Agents 编辑后经 `session.ReloadSkills()`/`ReloadAgents()` 热生效；MCP CRUD 写的是与 CLI 共享的用户级 `config.toml`，改动下个新会话生效。启动表单配置持久化在 `os.UserConfigDir()/runcode/desktop.json`（0600，可能含 API key），最近工作区列表由后端维护。

### 事件面（引擎 → 前端）

事件名与 payload 定义在 `internal/desktop/types.go`，经 `EventSink.Emit` 发出：

| 事件 | payload | 时机 |
|------|---------|------|
| `assistant:delta` | `AssistantDelta{text}` | 助手文本流 |
| `assistant:thinking` | `AssistantDelta{text}` | 思考流 |
| `tool:event` | `tool.Event`（原始引擎事件） | 工具生命周期泵 |
| `permission:request` | `PermissionRequest{id, summary, targets, externalTargets, externalRoots, command, harmReason}` | 审批挂起 |
| `turn:end` | `TurnEnd{text, stopReason, iterations, tokens…, stopped, durationMs}` | turn 完成 |
| `turn:error` | `TurnError{error}` | turn 失败/中断 |
| `warning` | `Warning{message}` | 引擎警告 |
| `session:renamed` | `SessionRenamed{id, title}` | 每 turn 后自动生成标题（30s 超时，持久化并广播） |
| `harm:autoallow` | `HarmAutoAllow{tool, operation, risk, reason, outcome, count}` | judge 模式智能放行/熔断审计 |
| `plan:updated` | `PlanRun{state, stage, doc, edited, updatedAt}` | 计划模式的阶段推进、用户编辑、确认或取消 |

`PermissionRequest.targets` 是工作区相对的脱敏路径；越出工作区的路径不再被丢弃，而是走 `externalTargets`（**绝对路径**，弹窗必须原样展示——用户被问的正是"要动项目外的哪个文件"），`externalRoots` 是选「本次会话 / 本项目」后真正记住的**目录**（含子目录），`summary.outsideWorkspace` 是这一事实的脱敏标记。`command` 仅 Bash 原始命令行、进程内展示用；`harmReason` 仅在 harm judge 升级拦截时携带。

`allowedDecisions` 列出这次请求真正能兑现的决定（`allow-once` / `allow-session` / `allow-project` / `deny`），**弹窗不得给出不在其中的选项**：没有授权键的动作（引擎侧 `grantSpec` 记不住）收到 `allow-session` 会照单接受然后什么都不记，用户于是"选了不再问、下次照问"。缺失该字段＝未声明，按老行为给全部四个；`deny` 永远在列——被限制的是"允许能记多远"，不是"能不能拒绝"。前端的取舍逻辑在 `shell/permission-decisions.ts`（纯函数，有单测），CLI 侧读同一事实的 `permissions.ApprovalRequest.Grantable`。

### 异步权限审批（Approver）

引擎 `host` 包的 `AsyncApprover`（会话装配时经 `host.SessionContext.Approver` 交给外壳）实现 `permissions.Approver`：executor 深处（turn goroutine）的每次阻塞审批分配 `perm-N` id，发 `permission:request` 事件后阻塞在 **buffered(1)** 应答 channel 上，直到 `Resolve(id, decision)`、`DenyAll()` 或 turn context 取消三者之一。保证每个挂起请求恰好被唤醒一次；缓冲 channel 使 `Resolve`/`DenyAll` 不会因接收方已离场而阻塞。未识别的 decision 一律按 deny 处理（fail-closed）。`Interrupt` 与 `CloseSession` 都会 `DenyAll` 兜底。并发工具的审批按 id 排队（前端同样排队，见下）。

### 配置映射与 harm judge 接线

`buildConfig`（`internal/desktop/config.go`）把 `StartSessionRequest` 映射为 `engine.Config`：CWD 必填并转绝对路径；model 缺省回退 `ANTHROPIC_MODEL`；权限模式校验 `safe|interactive|judge|flight`；thinking effort（off/low/medium/high）；`HarmJudgeModel`/`HarmJudgeVotes` 回退 `RUNCODE_HARM_JUDGE_MODEL`/`RUNCODE_HARM_JUDGE_VOTES`；MaxTokens 默认 16384、MaxIterations 固定 1000；固定 `PersistSession: true`、jsonl 会话后端、telemetry/transcript 关闭；单价经 `cost.Lookup` 自动填充；MCP 从共享 `config.toml` 加载且**错误吞掉**（坏 MCP 配置不阻塞开会话）。

harm judge 在 `buildAndSetLocked`（`internal/desktop/app.go`）接线：`permissions.Service` 始终安装 `InteractiveAuthorizer`（safe 起步也可运行时切换），并注入：

- `HarmJudge: modelHarmJudge` —— 用当前会话模型判定动作是否有害（`internal/desktop/harm.go`）。`describeAction` 把动作拆成**可信分类事实**（operation、命令分类/能力/风险原因、目标 scope）与**不可信原文**（命令行/路径/host/MCP 工具名），由会话侧做注入防护围栏。
- `Breaker: permissions.NewHarmBreaker(0)` —— 每会话自动放行预算/熔断器。
- `Audit` —— 每次智能放行或熔断触发发 `harm:autoallow`（脱敏：无原始命令/路径）。

### 阶段化计划模式（`internal/desktop/plan.go` + `internal/plantool`）

计划模式是一条固定流水线，不是"让模型自由写一段方案"：**需求理解 → 方案设计 → 方案审查 → 用户审批**。前三个阶段由模型经桌面专属工具 `plan_write` 逐个记录，第四个阶段不发模型——用户在审批板上改写、增删、调整顺序，确认后才退出计划模式开始执行。

- **阶段推进不靠外壳编排回合**：`plan_write` 每接受一个阶段，返回的结果文本就是下一阶段的指令，ReAct 循环自己在同一个回合里把三个阶段走完。顺序由工具把关（`planStore.RecordStage` 的闸门），跳阶段直接拒绝并告诉模型当前该做哪一阶段——"按步骤执行"因此是系统的性质，而不是对模型的期望。
- **重录早先阶段是允许的**（审查发现问题要改设计），但会把运行状态退回 planning，新设计必须重新过审查。
- **审批闸门**：三阶段跑完 → `awaiting_approval`，工具结果明确要求模型停下。用户点确认后 `PlanApprove` 一次做完三件事——存下用户这一版、退出计划模式并切到选定权限模式、拼出执行指令返回；指令由前端走普通 `send` 发出，于是 busy、用户气泡、回合生命周期全部复用既有链路。
- **执行进度复用既有进度胶囊**：执行指令要求模型先用 `TodoWrite` 建立与清单一一对应的待办，不另造一套进度 UI。
- **落盘**：`<工作区>/.runcode/plans/<sessionID>.json`，所以"等待审批"能跨重启活下来；恢复会话时 `PlanStatus` 把界面带回那道闸门。文件损坏一律当作没有计划，不影响开会话。
- 前端：进度条与审批板在 `chat/plan-board.tsx`，清单增删/排序的纯逻辑在 `chat/plan-draft.ts`（有单测），状态在 `session/use-plan.ts`。

### 桌面版 Skill 工具（`internal/skilltool`）

`configureSession` 经 `engine.Options.SkillTool` 把内置 Skill 工具换成桌面版——注意它与 `open_preview`/`ReadOffice` 走的不是同一条路：那两个经 `ExtraTools` **追加**，而会话内工具名唯一，同名的 `Skill` 只能**替换**（追加会让装配直接报重名失败）。

替换只接管"怎么披露"：内嵌引擎的 `*skill.Tool`，模型拿到的正文一字不差（含目录头、截断提示、大参考文档的委派建议、result retention 都是引擎的），只在成功加载后多发一条 progress 事件，`data` 是 `protocol.SkillLoad`（技能名/描述/来源 scope/技能目录/是否截断）。**有哪些技能仍归引擎**：发现、优先级、禁用名单、提示词目录、`ReloadSkills` 热更，都由引擎算好经 `SetSet` 下发（工具自己再存一份用于卡片查元数据，与内嵌工具同一个 set，否则卡片会描述另一个技能）。

前端据此把 Skill 调用单独渲染成技能卡（`chat/skill-card.tsx`，纯逻辑在 `chat/tool-text.ts` 的 `skillLoad`）：没有这条事件时，一次技能加载在界面上只是一行没有目标的"加载技能"——调用入参只有 name，而结果正文（给模型看的指令）从不渲染。恢复出来的历史没有实时事件，卡片退化成只显示名字。

### 会话列表与恢复

`ListSessions` 走工作区的 JSONL 会话后端，最新在前，标题取生成标题 → 末/首条用户提问 → id。`ResumeSession(id)` 复用已存的 provider/model/凭证配置重建会话，并把消息历史重建为前端 blocks（tool_result 按 id 与 tool_use 配对，恢复工具名/目标路径/原始输入；仅运行期才有的彩色 diff、文件 chips 不持久化），`contextTokens` 由 `Repl().EstimateContextTokens()` 估算。

## cmd/runcode-desktop：Wails 外壳

`main.go`：`//go:embed all:frontend/dist`；`eventSink` 把 `desktop.EventSink` 桥接到 `wruntime.EventsEmit`（OnStartup 前的事件丢弃）；`wailsDialog` 提供原生目录/图片选择器；窗口 1280×820（最小 1024×680）、**Frameless**（前端自绘标题栏与窗口控制）、`Bind: []any{app}`——前端调用落在 `window.go.desktop.App.*`。生成的绑定在 `frontend/wailsjs/`（git-ignored，wails 再生成）。

嵌套 module（`cmd/runcode-desktop/go.mod`，`replace github.com/wt68/runcode => ../..`；引擎 agentloop 走 require tag/go.work，无 replace）把 Wails/CGO/WebView 重依赖隔离在核心模块之外；核心 `go build ./...` 不拉 Wails。

## frontend：React + Vite + TS

扁平 `src/`：`App.tsx`（应用主体与聊天视图）、`pages.tsx`（设置/技能/代理/权限/MCP/工具/记忆页 + 启动表单）、`bridge.ts`（`window.go.desktop.App` 封装 + 事件名表 + TS 类型）、`chat.ts`（纯对话模型逻辑：block 分组、工具合并、plan 解析，Vitest 覆盖）、`markdown.tsx`（react-markdown + gfm + highlight.js）、`icons.tsx`、`ui.ts`、`styles.css`。状态全部在 `App()` 内 `useState`/`useRef`，无外部状态库；事件订阅在单个 `useEffect` 中折叠进 `blocks`。

主要视图/组件：

- **启动表单**（`StartForm`）：工作区选择 + 最近工作区、provider、权限模式、model、thinking 强度、**判定模型 + 判定表决（1/3/5 票）**、上下文预算、baseURL、API key。
- **应用壳**：侧栏（新建对话、8 项导航、最近会话恢复、切换工作区）；顶栏为可拖拽区 + 上下文用量表（≥80% 变琥珀色，`≈` 表示恢复估算，手动压缩按钮）+ 无边框窗口控制。
- **聊天视图**：用户气泡（含图片附件 chips）、助手 Markdown、notice/warning/error、每 turn 用量脚注。
- **工具卡片**（`ExecutionCard`/`ToolDetail`）：连续工具调用折叠为可扫读列表（图标+动词+目标+diff 徽标+状态），点开看输入/输出；Edit/Write 渲染**统一行内彩色 diff**。
- **子代理卡片**（`AgentTaskCard`）：Task 委托的实时嵌套视图（流式文本 + 子工具调用 + token/耗时）。
- **权限弹窗**（`PermissionModal`）：`harmReason` 红色"模型判定可能有害"横幅、原始命令块、摘要表、按 `allowedDecisions` 给出的决定（本次会话/仅此一次/本项目/拒绝，记不住的请求只剩后两个），有队列时另有"全部拒绝"；并发审批在前端按队列逐个展示。
- **进度板**（`PlanPill` 等）：顶部居中 pill，由 TodoWrite 快照驱动（步骤 N/M、改动文件数、diff 总量、时间线下拉）。
- **思考面板**：`ThinkingPanel` 可折叠思考链；`AnalyzeCard` 渲染结构化思考步骤。
- **输入区（composer）**：`@`（子代理）/`/`（技能）/`#`（文件）触发选择器、"+"添加菜单（技能/代理/文件/图片）、权限模式切换、plan 模式开关、思考强度菜单、模型 chip、发送/停止。
- **管理页**：Skills/Agents CRUD、Settings、Permissions（模式选择 + 操作×模式全矩阵，judge 模式的工作区变更标"允许 · 自动"）、MCP（完整 CRUD/启停）、Tools（内置/MCP 分组 + 并发标记）、Memory（可编辑项目指令 + 只读记忆）。

`harm:autoallow` 在前端渲染为 notice（"智能放行 · 工具（原因）· 本会话累计 N"），熔断触发时渲染 warning（"自动放行已达上限…转为逐个确认"）。

## 构建与 CI

- **正式打包**：`cd cmd/runcode-desktop && wails build` → `build/bin/XRUN.exe`（会跑 `npm install` + `npm run build` 重建前端）。
- **开发模式**：`wails dev`（Vite HMR + Go 后端）；启动表单留空时凭证/model 取自环境变量。
- **仅 Go 侧编译检查**：`go -C cmd/runcode-desktop build ./...`。`frontend/dist/` 整个是构建产物、一律不提交，只跟踪一个空的 `dist/.gitkeep` —— `main.go` 的 `//go:embed all:frontend/dist` 要求这棵树存在且至少匹配到一个文件，`all:` 前缀正是让点文件也算数的那一位，所以干净 clone 上 `go build` 仍能编译。真正的页面由 `npm run build` / `wails build` 再生成。
- **前端脚本**：`dev`=vite、`build`=vite build、`test`=vitest run。注意 `build` **没有 tsc 类型检查门禁**。
- **CI**（`.github/workflows/desktop.yml`）：ubuntu/windows/macos 三平台矩阵（Wails 不能交叉编译），Go 1.26.x + Node 20，Linux 加 webkit2gtk-4.1 依赖与 `-tags webkit2_41 -clean`，产物命名 `XRUN-{os}-{arch}`，tag 推送时附加到 GitHub Release。

## 当前缺口

- 工作区**文件树 + 文件预览**面板未实现（文件仅经 `#` 选择器暴露）。
- **side-by-side diff viewer** 未实现（现为统一行内 diff）。
- 图片**拖拽/粘贴**输入未实现（只有原生文件选择器路径）。
- 设置页未暴露 hooks 管理（MCP 已有）。
- `frontend/src/bridge.ts` 的 `StartSessionRequest` 类型缺 `harmJudgeModel`/`harmJudgeVotes` 字段，`pages.tsx` 已在使用——因 `vite build` 不做类型检查而未暴露，运行时正常（Go 侧字段齐全），应补齐类型。
