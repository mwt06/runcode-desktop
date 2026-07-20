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

Skills/Agents 编辑后经 `session.ReloadSkills()`/`ReloadAgents()` 热生效；MCP CRUD 写的是与 CLI 共享的用户级 `config.toml`，改动下个新会话生效。启动表单配置持久化在 `os.UserConfigDir()/runcode/desktop.json`（0600，可能含 API key），最近工作区列表由后端维护。

### 事件面（引擎 → 前端）

事件名与 payload 定义在 `internal/desktop/types.go`，经 `EventSink.Emit` 发出：

| 事件 | payload | 时机 |
|------|---------|------|
| `assistant:delta` | `AssistantDelta{text}` | 助手文本流 |
| `assistant:thinking` | `AssistantDelta{text}` | 思考流 |
| `tool:event` | `tool.Event`（原始引擎事件） | 工具生命周期泵 |
| `permission:request` | `PermissionRequest{id, summary, targets, command, harmReason}` | 审批挂起 |
| `turn:end` | `TurnEnd{text, stopReason, iterations, tokens…, stopped, durationMs}` | turn 完成 |
| `turn:error` | `TurnError{error}` | turn 失败/中断 |
| `warning` | `Warning{message}` | 引擎警告 |
| `session:renamed` | `SessionRenamed{id, title}` | 每 turn 后自动生成标题（30s 超时，持久化并广播） |
| `harm:autoallow` | `HarmAutoAllow{tool, operation, risk, reason, outcome, count}` | judge 模式智能放行/熔断审计 |

`PermissionRequest.targets` 是工作区相对的脱敏路径（越界路径丢弃）；`command` 仅 Bash 原始命令行、进程内展示用；`harmReason` 仅在 harm judge 升级拦截时携带。

### 异步权限审批（Approver）

`internal/desktop/approver.go` 实现 `permissions.Approver`：executor 深处（turn goroutine）的每次阻塞审批分配 `perm-N` id，发 `permission:request` 事件后阻塞在 **buffered(1)** 应答 channel 上，直到 `Resolve(id, decision)`、`DenyAll()` 或 turn context 取消三者之一。保证每个挂起请求恰好被唤醒一次；缓冲 channel 使 `Resolve`/`DenyAll` 不会因接收方已离场而阻塞。未识别的 decision 一律按 deny 处理（fail-closed）。`Interrupt` 与 `CloseSession` 都会 `DenyAll` 兜底。并发工具的审批按 id 排队（前端同样排队，见下）。

### 配置映射与 harm judge 接线

`buildConfig`（`internal/desktop/config.go`）把 `StartSessionRequest` 映射为 `engine.Config`：CWD 必填并转绝对路径；model 缺省回退 `ANTHROPIC_MODEL`；权限模式校验 `safe|interactive|judge|flight`；thinking effort（off/low/medium/high）；`HarmJudgeModel`/`HarmJudgeVotes` 回退 `RUNCODE_HARM_JUDGE_MODEL`/`RUNCODE_HARM_JUDGE_VOTES`；MaxTokens 默认 16384、MaxIterations 固定 1000；固定 `PersistSession: true`、jsonl 会话后端、telemetry/transcript 关闭；单价经 `cost.Lookup` 自动填充；MCP 从共享 `config.toml` 加载且**错误吞掉**（坏 MCP 配置不阻塞开会话）。

harm judge 在 `buildAndSetLocked`（`internal/desktop/app.go`）接线：`permissions.Service` 始终安装 `InteractiveAuthorizer`（safe 起步也可运行时切换），并注入：

- `HarmJudge: modelHarmJudge` —— 用当前会话模型判定动作是否有害（`internal/desktop/harm.go`）。`describeAction` 把动作拆成**可信分类事实**（operation、命令分类/能力/风险原因、目标 scope）与**不可信原文**（命令行/路径/host/MCP 工具名），由会话侧做注入防护围栏。
- `Breaker: permissions.NewHarmBreaker(0)` —— 每会话自动放行预算/熔断器。
- `Audit` —— 每次智能放行或熔断触发发 `harm:autoallow`（脱敏：无原始命令/路径）。

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
- **权限弹窗**（`PermissionModal`）：`harmReason` 红色"模型判定可能有害"横幅、原始命令块、摘要表、四个决定（本次会话/仅此一次/本项目/拒绝），有队列时另有"全部拒绝"；并发审批在前端按队列逐个展示。
- **进度板**（`PlanPill` 等）：顶部居中 pill，由 TodoWrite 快照驱动（步骤 N/M、改动文件数、diff 总量、时间线下拉）。
- **思考面板**：`ThinkingPanel` 可折叠思考链；`AnalyzeCard` 渲染结构化思考步骤。
- **输入区（composer）**：`@`（子代理）/`/`（技能）/`#`（文件）触发选择器、"+"添加菜单（技能/代理/文件/图片）、权限模式切换、plan 模式开关、思考强度菜单、模型 chip、发送/停止。
- **管理页**：Skills/Agents CRUD、Settings、Permissions（模式选择 + 操作×模式全矩阵，judge 模式的工作区变更标"允许 · 自动"）、MCP（完整 CRUD/启停）、Tools（内置/MCP 分组 + 并发标记）、Memory（可编辑项目指令 + 只读记忆）。

`harm:autoallow` 在前端渲染为 notice（"智能放行 · 工具（原因）· 本会话累计 N"），熔断触发时渲染 warning（"自动放行已达上限…转为逐个确认"）。

## 构建与 CI

- **正式打包**：`cd cmd/runcode-desktop && wails build` → `build/bin/XRUN.exe`（会跑 `npm install` + `npm run build` 重建前端）。
- **开发模式**：`wails dev`（Vite HMR + Go 后端）；启动表单留空时凭证/model 取自环境变量。
- **仅 Go 侧编译检查**：`go -C cmd/runcode-desktop build ./...`。`.gitignore` 提交了 `dist/index.html` 占位使 embed 可编译，`dist/assets/` 由 `npm run build` 再生成。
- **前端脚本**：`dev`=vite、`build`=vite build、`test`=vitest run。注意 `build` **没有 tsc 类型检查门禁**。
- **CI**（`.github/workflows/desktop.yml`）：ubuntu/windows/macos 三平台矩阵（Wails 不能交叉编译），Go 1.26.x + Node 20，Linux 加 webkit2gtk-4.1 依赖与 `-tags webkit2_41 -clean`，产物命名 `XRUN-{os}-{arch}`，tag 推送时附加到 GitHub Release。

## 当前缺口

- 工作区**文件树 + 文件预览**面板未实现（文件仅经 `#` 选择器暴露）。
- **side-by-side diff viewer** 未实现（现为统一行内 diff）。
- 图片**拖拽/粘贴**输入未实现（只有原生文件选择器路径）。
- 设置页未暴露 hooks 管理（MCP 已有）。
- `frontend/src/bridge.ts` 的 `StartSessionRequest` 类型缺 `harmJudgeModel`/`harmJudgeVotes` 字段，`pages.tsx` 已在使用——因 `vite build` 不做类型检查而未暴露，运行时正常（Go 侧字段齐全），应补齐类型。
