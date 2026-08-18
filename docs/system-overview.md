# 系统全览（方法 / 引用 / 抽象接口）

本文档是**机器扫描出来的全览图**，与手写的 [architecture.md](./architecture.md) 互补：那边讲"为什么这么分层"，这边讲"当前代码里实际有什么、谁引用谁、数字多大"。

数据来源不是 grep，而是 `go/types` 全量类型解析（`tools/sysmap`，与 gopls 同一套 `go/packages` 机制），所以接口实现关系、方法集、引用计数都是编译器认定的事实，不是文本匹配。

**本文数字的扫描日期：2026-08-03**（引擎 `agentloop v0.12.1`）。数字会随代码漂，改动较大后重扫一遍并更新本文：

```bash
go run ./tools/sysmap > sysmap.json     # 需要 go.work 联动 ../agentloop
```

命令还会往 stderr 打一行汇总（`pkgs=… ifaces=… types=… funcs=… lines=… errs=…`），可以先用它快速判断是否已经漂了。

> 曾经还从同一份 JSON 生成过两个可交互的单文件 HTML（全览页与手绘风格结构树），因为页面模板与装配脚本依赖 Node 侧外部工具、从未提交，生成物很快就与代码脱节，已移除。要重做的话，两个踩过的坑值得先知道：
>
> - **不要对 `mmdc`（`@mermaid-js/mermaid-cli`）产出的 SVG 做浮点精度压缩**。mermaid 用紧凑路径记法（`v-17l.001-.45`），四舍五入会写坏 `d` 属性，浏览器报 `<path> attribute d: Expected number`；只省 5%，不值得。`rough.js` 的输出是显式 `M x y C …`，压掉 62% 且安全。
> - **mermaid 的 `look: handDrawn` 效果不够**：只有连线弯、方框仍是直的，且 roughness 不可配；改用 `rough.js`（UMD，28 KB 内联）在浏览器里现画，整页 60 KB，而让 mermaid 预渲染同样五张图要 1.5 MB。

> 扫描口径：`go.work` 四个 module 的全部非测试包（`_test.go` 不计入）。引用计数 = `types.Info.Uses` 里指向该符号的标识符个数，**只含 Go 侧**——经 Wails/反射或 wire 协议跨语言的调用不在其中（这一点本身是个结论，见[Wails 边界](#wails-边界74-个命令60-个在-go-侧零引用)）。

## 一、规模盘点

| 指标 | 数量 |
| --- | --- |
| Go 包（非测试） | 59 |
| Go 代码行 | 49,709 |
| 抽象接口（含方法的 interface） | 50 |
| 具名类型 | 616 |
| 导出方法 | 543 |
| 包级导出函数 | 198 |
| 跨包引用边 | 184 |
| 前端 TS/TSX 模块 | 99（11,072 行，不计 `*.test.*` 与 `*.d.ts`） |

分层规模（行数占比）：

| 层 | 包数 | 行数 | 说明 |
| --- | --- | --- | --- |
| engine/support | 21 | 15,207 | `permissions`（3,485）`mcp`（2,688）`host`（1,408）`sessions`（1,213）`transcript` `telemetry` `skill` `memory` `agent` `settings` `protocol` `tool` … |
| engine/internal | 8 | 7,131 | ReAct 循环 `internal/repl`（4,875 行）、`internal/subagent`（720）、`internal/compaction`（540）、`internal/history`（439）、提示词装配 |
| engine/tools | 14 | 4,119 | 16 个内置工具，每包一个（`bash` 1,092 · `grep` 684 · `read` 355） |
| engine/llm | 3 | 3,857 | `llm` 契约（505）+ openai（2,652）/ anthropic（700）两个 provider |
| engine/core | 1 | 1,475 | `engine.Build` / `Config` / `Options` / `Session` 门面 |
| shell/desktop-core | 2 | 8,284 | `internal/desktop`（7,566 行）+ `internal/protocol`（718） |
| shell/tui | 2 | 3,252 | `internal/ui`（2,891）+ `internal/command`（361） |
| shell/cli | 1 | 2,393 | `cmd/runcode` 七个子命令 |
| shell/host-tools | 4 | 1,815 | `officetool`（1,329，ReadOffice）+ `plantool`（261，plan_write）+ `skilltool`（123）+ `previewtool`（102，open_preview） |
| shell/codegen | 1 | 1,138 | `tools/protogen` 协议代码生成器 |
| shell/server | 1 | 896 | `cmd/runcode-server` 骨架 |
| shell/desktop-main | 1 | 142 | Wails `main.go`，只做 `Bind` + 事件桥 |

**引擎 31,789 行 : 外壳 17,920 行**——把重的东西挪出去这件事，数字上确实发生了。

## 二、分层与依赖方向

```mermaid
flowchart TB
  subgraph FE["前端 React（99 模块，严格单向）"]
    direction TB
    F_pages["pages / shell / session"]:::fe
    F_mid["chat / preview / composer"]:::fe
    F_base["core（bridge + 生成的 protocol）· ui · hooks"]:::fe
    F_pages --> F_mid --> F_base
  end

  W["cmd/runcode-desktop<br/>Wails Bind + 原生对话框（142 行）"]:::shell
  APP["internal/desktop.App<br/><b>81 个命令方法</b>"]:::shell
  IPROTO["internal/protocol<br/>37 个桌面 DTO + CommandKinds"]:::shell
  CLI["cmd/runcode<br/>CLI 7 子命令"]:::shell
  TUI["internal/ui<br/>Bubble Tea"]:::shell
  SRV["cmd/runcode-server<br/>HTTP/SSE 骨架"]:::shell

  HOST["agentloop/host.Manager<br/>会话表 · 信封 · 审批路由 · 配额<br/>15 个方法"]:::eng
  EPROTO["agentloop/protocol<br/>wire 契约（9 个事件）"]:::eng
  SESS["engine.Session<br/>25 个方法（门面）"]:::eng
  REPL["internal/repl.Session<br/>ReAct 循环（私有）"]:::engi
  TOOLS["tools/* · mcp · skill · memory · subagent<br/>29 个 tool.Tool 实现"]:::eng
  LLM["llm.Provider<br/>anthropic · openai · openai-responses"]:::eng
  PERSIST["sessions · transcript · telemetry"]:::eng
  PERM["permissions<br/>Approver · Policy · HarmJudge · Authorizer"]:::eng

  F_base -.->|"Wails IPC（反射，非 Go 引用）"| W
  W --> APP
  APP --> IPROTO
  APP -->|54 refs| HOST
  APP -->|146 refs| SESS
  CLI --> TUI
  CLI -->|121 refs| SESS
  SRV --> HOST
  HOST --> EPROTO
  IPROTO -->|83 refs| EPROTO
  HOST --> SESS
  SESS --> REPL
  REPL --> TOOLS
  REPL -->|533 refs| LLM
  REPL --> PERSIST
  REPL --> PERM

  classDef fe fill:#1e3a5f,stroke:#4a90d9,color:#e8f0fa
  classDef shell fill:#3d2f1f,stroke:#c9922e,color:#faf3e8
  classDef eng fill:#1f3a2f,stroke:#3fa06a,color:#e8faf0
  classDef engi fill:#2a2a2a,stroke:#777,color:#ddd
```

扫描确认的两条硬纪律：

1. **引擎 → 外壳的引用边为 0**。184 条跨包边里没有任何一条从 `agentloop/*` 指向 `runcode/*`。
2. **`internal/repl` 只被 2 个包引用**（`agentloop` 根 67 次、`internal/subagent` 18 次）。ReAct 循环没有从 `internal` 泄漏出去，外壳只认 `engine.Session` 门面。

## 三、抽象接口（端口）全表

50 个含方法的 interface，其中 **32 个是跨层契约**（下表），18 个是包内私有的窄接口。按"跨包引用数"排序，越上面越是系统的关节。**加粗**的是外壳侧的实现（IoC 注入点）。

| 接口 | 方法 | 实现数 | 引用 | 谁实现 |
| --- | --- | --- | --- | --- |
| `tool.Tool` | 5 | **28** | 78 | 16 内置 + MCP 5 + Skill/Remember/Task + **officetool** · **plantool** · **previewtool** · **skilltool** |
| `llm.Provider` | 3 | 3 | 24 | anthropic.Provider · openai.Provider · openai.ResponsesProvider |
| `sessions.Backend` | 8 | 2 | 14 | sqliteBackend · jsonlBackend |
| `telemetry.Recorder` | 2 | 4 | 13 | AsyncRecorder · JSONLRecorder · MemoryRecorder · noopRecorder |
| `host.Session` | 13 | 1 | 13 | `*engine.Session`（存在纯粹为了让 host 可测） |
| `permissions.Approver` | 1 | 3 | 10 | **CLI 的 approvalPrompter** · **ui.Approver** · host.AsyncApprover |
| `hooks.Runner` | 1 | 3 | 10 | commandRunner · Noop · toolOnlyHooks |
| `sessions.Store` | 2 | 3 | 10 | JSONLStore · sqliteStore · noopStore |
| `host.Sink` | 1 | 2 | 9 | **desktop.hostSinkAdapter** · **server.hub** |
| `mcp.Launcher` | 1 | 0 | 8 | （端口预留：进程落在哪由宿主决定，本仓未实现） |
| `transcript.Recorder` | 2 | 3 | 7 | JSONLRecorder · SQLiteRecorder · noopRecorder |
| `llm.Stream` | 3 | 2 | 7 | anthropic.stream · openai.stream |
| `permissions.Authorizer` | 1 | 3 | 6 | Bypass · Interactive · NonInteractive |
| `permissions.SessionAllowStore` | 2 | 2 | 6 | FileAllowStore · MemorySessionAllowStore |
| `skill.Discloser` | 6 | 2 | 5 | **skilltool** · skill（渐进披露契约） |
| `desktop.EventSink` | 1 | 2 | 5 | **wails eventSink** · desktop.envelopeSink |
| `permissions.HarmJudge` | 1 | 1 | 5 | **desktop.modelHarmJudge** |
| `permissions.Resolver` | 1 | 2 | 5 | **desktop.planResolver** · DefaultResolver |
| `ui.Service` | 7 | 1 | 5 | cmd/runcode.tuiSessionService |
| `engine.Toolset` | 2 | 2 | 5 | localToolset · mcp.Manager |
| `permissions.Policy` | 1 | 1 | 4 | DefaultPolicy |
| `engine.SubagentLimiter` | 2 | 1 | 4 | host.semaphore（+ 结构等价于 subagent.Limiter） |
| `turn.EditHandle` | 1 | 1 | 3 | **desktop.editHandle** |
| `permissions.PersistentAllowStore` | 4 | 1 | 3 | FileAllowStore |
| `transcript.Reader` | 3 | 1 | 3 | SQLiteRecorder |
| `desktop.Dialoger` | 3 | 1 | 2 | **wails.wailsDialog** |
| `turn.EditRecorder` | 1 | 1 | 2 | **desktop.editStore** |
| `plantool.Store` | 1 | 1 | 2 | **desktop.planStore**（阶段化计划的阶段机） |
| `engine.ToolRuntime` | 1 | 1 | 2 | localRuntime（服务端宿主注入 gateway runtime） |
| `sessions.Deleter` | 1 | 2 | 1 | sqliteBackend · jsonlBackend |
| `tool.ResultRetentionProvider` | 1 | 8 | 1 | **skilltool** · skill · bash · edit · glob · grep · read · write |
| `tool.TurnReminderProvider` | 1 | 1 | 1 | todo |

余下 18 个是包内私有的窄接口（`mcp.messageStream`/`toolCaller`、openai 的 `sseStream`/`sseDecoder`/`doer`/`completionClient`/`responsesClient`、anthropic 的 `sdkStream`/`messageStreamClient`、CLI 的 `chatRunner`/`lineReader`/`tuiRunner`/`resettableChatRunner`、`repl.EditRecorder`/`EditHandle`、`subagent.Limiter`、`desktop.titleGenerator`）——它们是为了测试替身而存在的接缝，不是跨层契约。其中 `desktop.titleGenerator` 值得一提：它由 `*engine.Session` **结构匹配**满足，不是声明出来的依赖。

### 值得注意的三个形状

- **`tool.Tool` 是唯一的高扇出接口**（28 实现 / 26 个包引用）。工具执行位置无关这件事就靠它：本地工具、MCP 转发工具、沙箱转发工具在权限门、harm 判定、事件流面前完全同形。桌面专属的四个工具从外壳侧实现同一接口——`open_preview`、`ReadOffice`、`plan_write` 经 `Options.ExtraTools` **追加**，`skilltool` 经 `Options.SkillTool` **替换**内置 Skill（会话内工具名唯一，同名只能换不能加）。**外壳扩展引擎能力却不改引擎**，这是这套边界最值钱的一处。
- **`engine.SubagentLimiter` 与 `subagent.Limiter` 是一对孪生接口**。后者在 `internal` 包里，`Options` 语法上没法引用它，于是根包声明了一个同形的；`types.Implements` 确认两者互相满足。这是 Go 的 `internal` 约束逼出来的形状，不是冗余。
- **`mcp.Launcher` 有 8 处引用、0 个实现**。这是留给服务端宿主的端口（"MCP 进程起在哪"），本仓两个外壳都走默认本地子进程。同理 `ToolRuntime` 只有 `localRuntime`。

## 四、方法面：三个主类型

| 类型 | 导出方法 | 引用 | 位置 |
| --- | --- | --- | --- |
| `desktop.App` | **81** | 130 | `internal/desktop/app.go:55` |
| `engine.Session` | 25 | 32 | `agentloop/engine.go:182` |
| `repl.Session`（私有） | 21 | 76 | `agentloop/internal/repl/session.go:177` |
| `host.Manager` | 15 | 30 | `agentloop/host/manager.go:76` |
| `mcp.Client` | 11 | 27 | `agentloop/mcp/client.go:58` |
| `desktop.planStore` | 9 | 18 | `internal/desktop/plan.go:55` |
| `permissions.FileAllowStore` | 9 | 18 | `agentloop/permissions/persistent_allow.go:44` |

### `desktop.App` 的 81 个命令（按文件分工）

Wails 要求所有命令挂在同一个类型上，所以 `App` 必然是宽的；分工靠文件而非包：

| 文件 | 数 | 命令 |
| --- | --- | --- |
| `passport.go` | 10 | ActiveTenant · PassportCancelLogin · PassportLogin · PassportLogout · PassportModels · PassportStatus · PassportTenants · PassportValidate · SessionModels · SetActiveTenant |
| `app.go` | 7 | CloseSession · GetProtocolInfo · Reset · SetDialoger · StartSession · Startup · Status |
| `session_settings.go` | 7 | SaveSettings · SetModel · SetPermissionMode · SetPlanMode · SetReasoningScenario · SetThinkingEffort · SwitchModel |
| `sessions.go` | 7 | DeleteSession · ListSessions · ListTools · NewSession · PickWorkspaceFolder · ResumeSession · SwitchWorkspace |
| `mcp.go` | 5 | DeleteMCPServer · ListMCPServers · ReloadMCPServers · SaveMCPServer · SetMCPServerEnabled |
| `turn.go` | 5 | Compact · InjectMessage · Interrupt · ResolvePermission · SendMessage |
| `agents.go` / `skills.go` | 4 + 4 | Delete/Import/List/Save × {Agent, Skill} |
| `plan.go` | 4 | PlanApprove · PlanCancel · PlanStatus · PlanUpdate |
| `attachments.go` | 3 | InjectMessageWithImages · PickImageAttachment · SendMessageWithImages |
| `context.go` | 3 | ReadMemory · ReadProjectContext · SaveProjectContext |
| `custommodels.go` | 3 | DeleteCustomModel · ListCustomModels · SaveCustomModel |
| `disabled.go` | 3 | SetAgentEnabled · SetSkillEnabled · SetToolEnabled |
| `editstore.go` | 3 | ListEdits · RevertEdit · ReviewEdit |
| `open.go` | 3 | OpenExternal · ResolveArtifactPath · RevealInFolder |
| `contextaudit.go` / `preview.go` / `webproxy.go` | 2 × 3 | ContextAuditStatus/SetContextAudit · ReadArtifact(Bytes) · SetWebProxy/WebProxy |
| `files.go` / `mcpmarket.go` / `officepdf.go` / `store.go` | 1 × 4 | ListFiles · McpMarket · RenderOfficePDF · LoadConfig |

### Wails 边界：81 个命令，67 个在 Go 侧零引用

扫描出的最有意思的一条：**81 个命令方法里有 67 个的 Go 侧引用数为 0**。它们不是死代码——它们全部经 Wails 的反射绑定被前端调用，编译器看不见这条边。

这正是 `tools/protogen` 必须存在的理由：`internal/protocol.CommandKinds`（26 query / 28 idempotent-set / 25 trigger，共 79 条登记）与 `App` 的导出方法互相交叉核对，方法漏登记或登记了不存在的方法都让生成失败，CI 用 `--check` 把关。**编译器管不到的那条边，由代码生成器管。**（81 − 79 = 2 个方法不上 wire：`Startup` 与 `SetDialoger` 是外壳内部接线。）

## 五、跨包引用强度（Top 20 边）

| 引用数 | 边 | 读法 |
| --- | --- | --- |
| 619 | `desktop → internal/protocol` | **已升为第一大边**：桌面 DTO 都在自己家，没漏进引擎 |
| 533 | `repl → llm` | ReAct 循环几乎全在搬 llm 类型，正常 |
| 411 | `openai → llm` | provider 适配 |
| 319 | `repl → tool` | 工具调度 |
| 212 | `repl → telemetry` | 埋点密度较高 |
| 186 | `anthropic → llm` | provider 适配 |
| 178 | `mcp → tool` | MCP 工具适配到统一契约 |
| 146 | `desktop → engine`(根) | Config/Options/Status |
| 141 | `tools/bash → tool` | 三件套工具 |
| 139 / 130 | `host → protocol` / `server → protocol` | 两个数字接近——服务端骨架确实只吃 wire 契约 |
| 121 | `cmd/runcode → engine`(根) | CLI 直接拼 Session（单会话，不过 host） |
| 113 | `desktop → llm` | 消息/附件类型 |
| 111 / 91 | `compaction / history → llm` | 上下文治理两个新包，都在搬消息 |
| 86 / 85 / 74 | `desktop / cli / ui → permissions` | 三个外壳各自桥接审批 |
| 83 | `internal/protocol → agentloop/protocol` | 桌面 DTO 复用 envelope/错误码 |
| 67 | `engine(根) → internal/repl` | 门面唯一的入口 |
| 54 | `desktop → host` | 薄适配层，数字小是对的 |
| 4 | `cmd/runcode-desktop → internal/desktop` | Wails 层只有 `Bind` 那几行 |

`desktop → host` 只有 54 次引用，而 `desktop → engine 根` 有 146 次：桌面在会话生命周期上依赖 host，在配置/状态类型上依赖引擎根包。

## 六、一次回合的数据流

```mermaid
sequenceDiagram
  autonumber
  participant FE as 前端 core/bridge
  participant W as Wails
  participant A as desktop.App
  participant M as host.Manager
  participant S as engine.Session
  participant R as repl（ReAct）
  participant P as llm.Provider
  participant T as tool.Tool

  FE->>W: SendMessage(text)
  W->>A: 反射调用（Go 侧零引用的那条边）
  A->>M: SendMessage(id, text)
  M->>M: 取并发槽（满则发 turn:queued）
  M->>S: RunTurn(ctx, text)
  S->>R: 进入 ReAct 循环
  loop 直到无工具调用
    R->>P: Stream(messages, tools)
    P-->>R: delta / thinking / tool_use
    R-->>M: StreamDelta / StreamThinking
    M-->>FE: assistant:delta · assistant:thinking（带 Seq）
    R->>R: permissions 门 + HarmJudge
    opt 需审批
      R-->>M: Approver.Prompt
      M-->>FE: permission:request
      FE->>A: ResolvePermission(id, decision)
      A->>M: 解析 → 放行/拒绝
    end
    R->>T: Run(input, tctx, out)
    T-->>M: tool.Event（started/progress/output/completed）
    M-->>FE: tool:event
  end
  R-->>S: turn.Result
  S-->>M: 结果
  M-->>FE: turn:end（或 turn:error）
  M->>A: OnTurnEnd → 自动标题
  A-->>FE: session:renamed
```

事件面共 13 个：引擎 9 个（`assistant:delta` `assistant:thinking` `context:usage` `llm:retry` `permission:request` `tool:event` `turn:end` `turn:error` `turn:queued`）+ 桌面自有 4 个（`harm:autoallow` `passport:changed` `plan:updated` `session:renamed`）。

## 七、工具面：模型能看到什么

`tool.Tool` 的 28 个实现分四类来源：

```mermaid
flowchart LR
  R["repl 的 toolset"]:::c
  B["内置 16 个<br/>tools.BuiltinsWithConfig（顺序固定，重名即报错）"]:::b
  D["动态 3 个<br/>Task（子代理）· Skill · Remember"]:::d
  M["MCP 5 个包装<br/>mcpTool · listResources · readResource · listPrompts · getPrompt"]:::m
  X["外壳注入 4 个（仅桌面）<br/>open_preview · ReadOffice · plan_write<br/>+ skilltool（替换内置 Skill）"]:::x
  B --> R
  D --> R
  M --> R
  X -->|"Options.ExtraTools 追加 / Options.SkillTool 替换<br/>（子代理快照之后，故子代理拿不到）"| R
  classDef c fill:#1f3a2f,stroke:#3fa06a,color:#e8faf0
  classDef b fill:#1e3a5f,stroke:#4a90d9,color:#e8f0fa
  classDef d fill:#3d2f1f,stroke:#c9922e,color:#faf3e8
  classDef m fill:#3a1f3a,stroke:#a04a9e,color:#fae8f8
  classDef x fill:#3a1f1f,stroke:#c04a4a,color:#fae8e8
```

内置 16 个（`tools/registry.go` 的固定顺序）：`Read` `Write` `Edit` `Delete` `Glob` `Grep` `Bash` `BashOutput` `KillShell` `TodoWrite` `WebFetch` `WebSearch` `Analyze` `AskUser` `Wait` `GetCurrentTime`。

## 八、前端分层（扫描确认单向）

99 个 TS/TSX 模块（不计 `*.test.*` 与 `*.d.ts`），`@/` 别名的实际引用方向：

```mermaid
flowchart TB
  P["pages（22）"]:::l4
  SH["shell（6）"]:::l4
  SE["session（9）· 引擎事件订阅在此"]:::l4
  CH["chat（18）"]:::l3
  PV["preview（11）"]:::l3
  CO["composer（5）"]:::l3
  UI["ui（13）"]:::l2
  CORE["core（10）· bridge + 生成的 protocol/"]:::l1
  HK["hooks（2）"]:::l2

  P --> UI & CORE
  SH --> CH & PV & UI & CORE
  SE --> CH & PV & UI & CORE & HK
  CH --> PV & UI & CORE & HK
  CO --> PV & UI & CORE
  PV --> UI & CORE
  UI --> CORE

  classDef l1 fill:#1e3a5f,stroke:#4a90d9,color:#e8f0fa
  classDef l2 fill:#1f3a4f,stroke:#4a90d9,color:#e8f0fa
  classDef l3 fill:#1f3a2f,stroke:#3fa06a,color:#e8faf0
  classDef l4 fill:#3d2f1f,stroke:#c9922e,color:#faf3e8
```

被引用次数：`@/ui` 91 · `@/core` 85 · `@/chat` 24 · `@/preview` 14 · `@/session` 10 · `@/shell` 6 · `@/hooks` 6 · `@/pages` 5 · `@/composer` 1。**没有一条反向边**（`core` 只引 `@/assets`，`ui` 只引 `@/core`，`hooks` 谁都不引）。

## 九、扫描口径与已知盲区

- **只覆盖 Go 与 TS 的静态结构。** 运行期通过反射（Wails 绑定）、wire 协议（前后端）、`os/exec`（MCP stdio、hooks）连起来的边，静态分析看不见——这些边分别由 `protogen --check`、`deps_test.go`、集成测试兜。
- **引用计数不含测试文件。** `_test.go` 被排除，所以"零引用"不代表"零测试"；`desktop.App` 的 130 次引用里大部分来自非测试代码路径。
- **`sessions/backendtest` 是测试工具包**，虽非 `_test.go` 但只服务测试，计入了包数与行数。
- **依赖解析用 export data，不是全源码类型检查**（`packages.NeedDeps` 关掉了：开着会连 `modernc.org/libc` 一起从源码类型检查，慢到不可用）。这不影响本仓与引擎符号的解析精度——初始包全部是源码级类型检查。
- **地图不含制图者。** `tools/sysmap` 把自己排除在扫描之外（`inWorkspace` 里显式跳过），否则每次改这个工具本身，全表数字都会漂。`tools/protogen` 不排除——它是构建契约的一部分，CI 会跑。
- **本次没走 LSP。** 环境未配置 Go 语言服务器、也没装 `gopls`，因此直接调用 `go/packages` + `go/types`——与 gopls 底下同一套机制，只是省掉了 LSP 那层协议。
