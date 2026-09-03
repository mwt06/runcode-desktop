# 多工作区 · 多会话并行 —— 实施方案

> 目标：桌面版能同时打开多个会话，每个会话可以在**各自的工作区**里独立跑任务，互不阻塞。
>
> 本文是实施方案，不是已完成的设计记录。落地时如果发现与现状不符，以代码为准并回来改这份文档。

---

## 一、结论先行

**引擎（`../agentloop`）一行都不用改。**

`host.Manager` 本来就是多会话的：内部有 `sessions map[string]*hostSession`（`host/manager.go:94`），每个会话有自己的回合 goroutine（`runTurn`）、自己的信封序列、自己的配置与权限服务，所有公开方法（`SendMessage` / `Interrupt` / `ResolvePermission` / `SetModel` / `SetPermissionMode` / …）**第一个参数就是会话 id**。`cmd/runcode-server` 正是按多会话在用它。

挡住并行的是两层外壳：

| 层 | 约束 | 位置 |
| --- | --- | --- |
| 桌面外壳 | 显式的 "single-active-session rule"，一切命令经 `currentID` 隐式寻址 | `internal/desktop/app.go:51`、`:80` |
| 前端 | 一份全局对话状态，事件处理器完全无视 `sessionId` | `session/use-conversation.ts` |

所以这件事的性质是**外壳去单例 + 前端多路化**，不是"给引擎加并发"。

---

## 二、现状盘点

### 2.1 引擎侧已具备（可直接复用，不要重造）

- 会话表、每会话回合 goroutine、每会话信封序列（`seq` 各自独立）。
- `Options.Configure`（外壳的 `configureSession`，`app.go:277`）**每会话调一次**：允许清单、权限服务、工具集都已经是每会话一份。
- `host.Limits`：`MaxSessions`、`IdleTimeout` 都支持；桌面当前传的是零值（无限制、不回收）。
- `turn:queued` 事件已存在——"回合排队"的语义两端都认。

### 2.2 外壳的单例点

`App` 上这些字段全部是"那一个会话"的，需要搬进 per-session 表：

| 字段 | 含义 | 备注 |
| --- | --- | --- |
| `currentID` | 唯一活动会话 | 降级为"当前聚焦的会话"，只服务 UI |
| `turnActive` | 该会话是否有回合在跑 | 每会话一份 |
| `lastUserText` | 喂自动标题 | 每会话一份 |
| `emit` / `pendingEmit` | 该会话的信封发射器 | 每会话一份 |
| `workspace` | 该会话的工作目录 | 每会话一份（**跨工作区并行的核心**） |
| `preview` / `previewURL` | 工作区静态预览服务器 | 改为按工作区共享 + 引用计数 |
| `edits` / `pendingEdits` | 「已编辑」卡片的撤销/复审存储 | 概念上早已是每会话一个，只是 App 只存了一份 |
| `plans` / `pendingPlans` | 阶段化计划模式的运行态与审批闸门 | 同上 |
| `config` / `liveConfig` / `livePassport` / `livePassportTenant` / `passportTenant` | 连接配置 | **本期保持进程级**，见 §3.5 |

`currentID` 的读取点约 25 处，分布在 `app.go`、`attachments.go`、`custommodels.go`、`mcp.go`、`passport.go`、`plan.go`、`session_settings.go`、`sessions.go`、`title.go`、`turn.go`。

命令面（`internal/protocol/commands.go`）里**没有任何一条命令带会话 id**。

好消息：`hostSinkAdapter.Emit` 已经在按 `env.SessionID` 处理回合结束（`app.go:200` 附近的 `noteTurnDone(env.SessionID)`），说明"事件自带会话身份"这条路是通的。

### 2.3 前端的单例点

- `session/use-conversation.ts`：11 个 `onEvent(...)` 处理器，**全部只拿载荷、不看会话**，一律写进唯一一份状态。
- `session/use-permission-queue.ts`：一个全局队列；`resolvePermission(id, decision)` 只带 requestID，后端在 `turn.go:124` 拿 `currentID` 去解析——**并行后这里会把审批解到错误的会话上**。
- `usePreviewPanel` / `useWorkspaceFiles` / `usePlan` / 输入框草稿：都是单份。
- `shell/sidebar.tsx`：已有会话列表与 `onResume`，但语义是"换成这一个"，不是"同时开着"。

好消息：`core/protocol/events.ts` **已经生成了 `onEnvelope`**，交付完整信封（含 `sessionId`、`seq`、`ts`）。前端分流不需要改协议层，只需要把 `onEvent` 换成 `onEnvelope` 并按 id 派发。

---

## 三、设计

### 3.1 外壳：per-session 表

```go
// sessionEntry 是一个会话在外壳侧的全部状态。
// 它不持有引擎的东西——引擎那份在 host.Manager 的会话表里，靠 id 关联。
type sessionEntry struct {
    id        string
    workspace string           // 该会话的工作目录
    emit      func(string, any)
    edits     *editStore
    plans     *planStore

    mu           sync.Mutex    // 只保护下面这几个可变字段
    turnActive   bool
    lastUserText string
    title        string
}
```

`App` 上把上述字段换成：

```go
mu       sync.Mutex
sessions map[string]*sessionEntry
focused  string   // 仅用于「命令没带 id 时打给谁」与 UI 高亮
```

**锁序（必须写进注释，这是最容易出并发事故的地方）**：
`startMu` → `App.mu` → `sessionEntry.mu`。任何路径都不得反向获取，也不得在持有 `App.mu` 时等待 `startMu`（现有注释已有这条，扩展到第三层）。`recorderCtl` 仍然独立、不参与这个序。

### 3.2 命令寻址

所有会话相关命令加一个 `sessionId` 参数。命令清单在本仓（`internal/protocol/commands.go`），**加参数不用给引擎发版**；`protogen` 会拿这张表和 `desktop.App` 的导出方法交叉核对，漏改会直接构建失败，CI 用 `--check` 把关——这是免费的护栏，务必依赖它。

> **修正（P0 实施时发现）**：本节原先写的是"P0 加参数、空串回落聚焦会话，前端一行不改也能跑"。**这条对 TypeScript 不成立**——protogen 从 Go 方法签名生成 TS，加一个参数就是必填参数，所有前端调用点当场编译失败，等于把 P1 的前端改动拽进 P0。
>
> 因此**线上契约的改动整体挪到 P1**，与前端分流同一次合入；P0 是纯内部重构，命令签名一个都不动。空串回落的过渡策略在 P1 内部仍然有用（分批改调用点时）。

受影响的命令（非穷举，实施时以 `currentID` 的引用点为准）：
`SendMessage` / `SendMessageWithImages` / `InjectMessage` / `Interrupt` / `ResolvePermission` / `Compact` / `SwitchModel` / `SetPermissionMode` / `SetPlanMode` / `SetThinkingEffort` / `SetReasoningScenario` / `ListEdits` / `ReviewEdit` / `UndoEdit` / `PlanStatus` / `ApprovePlan` / `CancelPlan` / `ListFiles` / `PickImage`…

### 3.3 工作区与预览服务器

每会话一个 `workspace`。预览服务器**按工作区共享**，不是按会话：

```go
previews map[string]*previewRef   // key = 工作区绝对路径
type previewRef struct {
    srv  *previewServer
    url  string
    refs int          // 引用它的会话数；归零才 stop
}
```

`startPreview(workspace)`（`preview.go:125`）现在是"停掉旧的、起一个新的"，要改成"查表命中就 +1，没有就起一个"。关会话时 -1，归零才停。

`SwitchWorkspace` 的语义要拆开：现在它是"把整个 App 换到另一个目录"，之后应该是 `OpenSession(workspace)`——在指定工作区开一个**新**会话，不影响已开的。旧命令保留为"把聚焦会话换到另一个工作区"或直接废弃，二选一，实施时定。

前端 `useWorkspaceFiles` 同理，改成按会话（实际按 cwd）一份，同工作区的会话可以共享同一份索引。

### 3.4 事件分流

前端：

```ts
// 一会话一份状态，聚焦的那个才渲染
const [convs, setConvs] = useState<Map<string, ConversationState>>(new Map())
onEnvelope(Events.AssistantDelta, (env) => patch(env.sessionId, ...))
```

要点：

- **后台会话的增量照收不误，但不进渲染路径**。多个会话同时流式输出时，若每条 delta 都触发聚焦会话之外的重渲染，界面会明显发卡。用 `Map` + 仅对聚焦 id 做 `useSyncExternalStore` 式订阅，或把非聚焦会话的更新合批。
- 信封自带 `seq`（每会话独立递增），乱序或重复到达时按 seq 去重——现在单会话下没暴露过这个问题，多会话下事件量上来会。
- 进程级事件（`passport:changed`）的 `sessionId` 是空串，派发时要单独处理，不要当成"某个会话"。

### 3.5 连接配置（本期不拆，但要说清楚）

本期**所有并行会话共用同一套连接**（provider / 通行证租户 / baseURL / apiKey）。

需要区分两件事，否则容易误判：

- **模型是每会话的**——`Manager.SetModel(id, model)` 本来就按会话生效，外壳只是用 `currentID` 寻址而已。改完寻址，"A 会话用 k3、B 会话用别的"**自动就能用**。
- **连接是进程级的**——`liveConfig` / `livePassport` / `livePassportTenant` 描述的是 manager 当前打开的那条连接。切换租户会重建它，进而影响所有会话。

因此本期的行为约定：**设置里改 provider/租户，只对之后新建的会话生效；已开的会话继续用原连接。**`SwitchModel`（`session_settings.go:147`）里那段"跨租户要重建连接"的逻辑在多会话下必须加一道闸门：有其他会话正在跑回合时，不允许做会重建连接的切换，直接把原因告诉用户，而不是静默把别人的会话打断。

每会话独立连接留作后续（见 §6）。

### 3.6 审批分流与可见性（**最容易做漏、后果最重**）

现状是一个全局队列 + 后端拿 `currentID` 解析。并行之后有两个必须解决的问题：

1. **解错人**：B 会话弹出的授权请求，用户在 A 会话点了允许，后端会解到 A 上。→ `ResolvePermission` 必须带 `sessionId`；前端队列改成 `Map<sessionId, PermissionRequest[]>`。
2. **看不见**：后台会话卡在等审批时，界面上毫无表示，用户会以为它在跑。→ 会话列表项上必须有**待审批角标**，并且在全局位置（标题栏或状态栏）给一个"N 个会话在等你"的入口。

第 2 条不做，"并行"就是个陷阱：任务停在那里而用户不知道，比不能并行更糟。

### 3.7 录音纪要与多会话

录音是**进程级单例**（一次只允许一场，`recorderCtl` 自带锁，与会话锁无嵌套关系）。这条不变。

但录音卡片是钉在对话末尾的（`ChatPane` 的 `recorderCard`），所以要记录**这场录音属于哪个会话**：开始录音时记下当时聚焦的会话 id，卡片只在那个会话里渲染。否则切到别的会话会看见一张不属于它的录音卡。

### 3.8 会话列表要多带的状态

`protocol.SessionSummary` 现在只有 `{ID, Title, When, Turns}`（`internal/protocol/session.go:100`），是给"最近会话"列表用的。多会话 UI 需要区分**打开中**与**历史**，并展示运行态：

```go
type OpenSession struct {
    ID          string
    Title       string
    Workspace   string
    State       string  // idle | running | awaiting-approval | error
    PendingReqs int
    LastError   string
}
```

---

## 四、分期

每一期都必须是可构建、可验证、可单独合入的。

### P0 — 外壳去单例（前端不动，线上契约不动，行为不变）✅ 已完成

- `sessionEntry` + `sessions map[string]*sessionEntry` + `focused`，§2.2 的每会话字段全部搬进条目。
- 条目可变字段（`turnActive` / `lastUserText` / `closed`）的读写收在 app.go 的「会话表的读写口」一节：`entryLocked` / `liveEntryLocked` / `liveEntry` / `liveSessionIDOrEmpty` / `focusedSessionID` / `turnInFlight` / `focusedStores` / `focusedPlansAndSession`。别处不直接摸。
- 关闭改为**标记 `closed` 而不是删条目**，`focused` 也不动——「已编辑」卡片在会话关闭后仍可复审，靠的就是这个；下一个会话开出来时 `dropClosedLocked` 才清。
- `noteTurnDone` / `onTurnEnd` / `refreshTitle` 改为**按信封里的会话 id** 取，不再是"等于当前会话才算"。这一步之后后台会话的回合结束与标题生成就是对的。
- 预览服务器改成按工作区引用计数（`previews map[string]*previewRef`）；`previewBaseURL()` 报的是聚焦会话那个工作区的地址。
- 锁序写进 `sessionEntry` 的文档注释：条目不带自己的锁，可变字段一律由 `App.mu` 保护；将来真需要时按 `startMu → App.mu → entry.mu`。

**命令签名一个都没动**，所以前端不用改、行为不变。

**验收**（已通过）：`go test -race ./internal/...` 全绿；`golangci-lint ./internal/...` 0 issues；`protogen --check` 不漂移；`go -C cmd/runcode-desktop build ./...` 通过；打包后手动跑通。

**新增测试**（`session_table_test.go`、`preview_lifecycle_test.go`）：
- `TestNoteTurnDoneIsPerSession`——后台会话的回合结束只清它自己那条记账，不动聚焦会话。
- `TestClosedSessionKeepsStoresUntilReplaced`——关闭后命令层看成"没有会话"，但编辑存储还在、`focused` 不动，直到 `dropClosedLocked`。
- `TestFocusedStoresNeverNil`——第一个会话开出来之前读存储不会拿到 nil。
- `TestPreviewServerSharedPerWorkspace`——同一工作区只起一台服务器；关掉其中一个会话时另一个的预览不断，引用归零才停。

「两个会话各跑一个回合、事件不串台、审批解到正确会话」这一条要等 P1 命令能寻址之后才测得到，挪到 P1 的验收里。

### P1 — 前端按会话分流（UI 仍单会话）

分成两步，第一步已完成。

#### P1a — 事件与状态按会话分流 ✅ 已完成

- 新增 `session/conversation-state.ts`（纯逻辑，带单测）：`ConversationState`、`ConvMap`、`patchConv` / `convOf` / `dropConv` / `withReverted` / `sessionOf`。
- `use-conversation` 的 9 个独立 `useState` 收成**每会话一份** `ConvMap`，全部 `onEvent` 换成 `onEnvelope`，事件按 `sessionOf(env)` 落。渲染只取聚焦会话那一份。
- `busy`、`userStopped` 都按会话记：后台会话在跑不该让前台输入框变成"回合进行中"；A 会话按停止也不该让 B 会话的失败被静默吞掉。
- 权限队列（`use-permission-queue`）改成每会话一条，`enqueue(sessionID, req)` / `clear(sessionID)`；另外导出 `waiting`（每会话还有几个在等），供 P2 的角标直接用。
- `use-plan` 只接受**当前会话**的 `plan:updated`。计划板是后端权威状态（切会话时 `planStatus()` 回读），前端不必再缓存一份，但必须挡住别的会话——否则后台会话记一个阶段就把前台的板子盖掉。
- `App.tsx` 新增 `focusedId` 状态（不是 ref：状态每会话存一份之后，"看的是哪条"必须是响应式的值，ref 变了不会重渲染）。

**两处实施时才暴露的坑**（都已修，值得记住）：

1. **`applyResumed` 不能按"聚焦会话"写。** `use-session` 里 `setInfo(r.info)` 与 `applyResumed(r)` 是同步相邻的两句，那一刻 React 还没重渲染，聚焦 id 仍指着**上一条**会话——按它写就是把整段历史塞进别人的对话，而恢复出来的这条看着空空如也。现在从恢复载荷自带的 `r.info.sessionId` 取。
2. **`reset()` 的语义反过来了。** 状态按会话存之后，新会话的 id 在表里本来就没有条目，取到的就是空状态，不需要"清空"；要做的是把**被替换掉的那条**从表里删掉，否则每换一次会话就留一份再也不会被看到的历史在内存里。它跟在 `setInfo` 之后同步调用，那一刻聚焦 id 正好还指着要删的那个。

**验收**（已通过）：`typecheck` / `lint`（无 disable）/ `vitest` 283 用例 / `npm run build` 全绿；打包后手动跑通。

一处值得记的工程细节：`patch` / `blocksOf` 用了 `useCallback`。事件订阅只能注册一次，而 `exhaustive-deps` 会要求把它们列进依赖——每次渲染重建的话，列进去就等于每次渲染都重订阅（漏事件 + 重复处理），不列又要压规则。固定引用让"列进依赖"与"只订阅一次"同时成立，不需要 `eslint-disable`。

#### P1b — 命令带 `sessionId` ✅ 已完成

23 个会话寻址命令加了首参 `sessionID`：`SendMessage` / `SendMessageWithImages` / `InjectMessage` / `InjectMessageWithImages` / `Interrupt` / `ResolvePermission` / `Compact` / `Reset` / `Status` / `SetPermissionMode` / `SetModel` / `SwitchModel` / `SetPlanMode` / `SetReasoningScenario` / `SetThinkingEffort` / `PlanStatus` / `PlanUpdate` / `PlanApprove` / `PlanCancel` / `ListEdits` / `RevertEdit` / `ReviewEdit`。

**工作区寻址的那批没动**（`ListFiles`、`ReadArtifact`、技能、子代理、MCP、记忆）——它们跟的是目录不是会话，属于 P3。混进来只会让这次改动失去边界。

解析口统一在 `App.entryOf(sessionID)`：

- **空串 = 聚焦会话**。界面上大部分动作作用于用户正看着的那条，让每个调用点自己去查一遍 id 只是噪音。
- **未知或已关闭的 id 一律 `errNoSession`**，绝不悄悄退回聚焦会话——那正是要消灭的行为。
- 配套的 `sessionIDOf` / `storesOf` / `plansAndSessionOf` / `editStoreOf` / `sessionHandleOf` / `engineSessionOf` 都是同一套规则。

**验收**（已通过）：`go test -race ./internal/...` 全绿；`golangci-lint` 0 issues；`protogen --check` 不漂移；前端 `typecheck` / `lint` / `vitest` 283 用例全绿；打包跑通。行为不变。

新增 `TestCommandsAddressTheGivenSession`：空串解析到聚焦会话、显式 id 解析到指定会话（哪怕它不是聚焦的那条）、未知 id 报错不回退、已关闭的会话报错、编辑与计划存储都按 id 取。

**一处 lint 抓到的真错误值得记**：`ReviewEdit` 改了签名但函数体还在调 `a.editStore()`（聚焦会话），`revive` 的 unused-parameter 把它揪了出来。这正是这次改动要消灭的那类 bug——参数在、语义没跟上，运行时不会报错，只会读到别人的数据。

#### P1c — 补上端到端的并行验证 ✅ 已完成

P1b 交付时欠着一条：「两个会话各跑一个回合、事件不串台」当时只测到了寻址层。现在补齐了。

**做法**：`New` 之外加一个 `newWithBuild(sink, host.BuildFunc)`。引擎的 `host.BuildFunc` 本来就写着 *tests inject a fake*，是桌面把 `host.DefaultBuild` 写死在 `New` 里堵上了这个口子。生产上只有一个 build，测试里换成可控的替身。

替身**只替换"跑一个回合"这一步**——会话编号、事件发射、每会话信封序列、回合记账全部由真的 `host.Manager` 负责，那些正是要验的东西。

顺带把 `registerSessionLocked` 从 `openSessionWithConnectionHeld` 里拆了出来：它只管"登记一条会话并聚焦"，**不关掉别的会话**。"先关掉当前那条"是调用方的单会话策略，不是登记这件事本身的一部分。P2 要的正是前者不带后者。

**三条测试**（`parallel_sessions_test.go`）：

- `TestTwoSessionsRunTurnsInParallel`——核心。给 A 发消息、等它真的开跑（此时它阻塞着），再给 B 发消息、等它也开跑。**B 能在 A 阻塞期间跑起来，这本身就是"并行"而不是"排队"的证明**（若管理器有全局回合信号量，B 永远等不到）。然后只放行 A：`turn:end` 只带 A 的会话 id，A 的在途标记清了而 B 的还在。
- `TestInterruptOnlyStopsTheAddressedSession`——打断 A 不会把 B 的活掐掉。
- `TestSendMessageRejectsUnknownSession`——寻址错了报错，且消息**没有**落到聚焦会话上。最后这条断言是关键：静默回退才是最坏的失败，两边都不报错，用户只会看到"我明明在这条对话里发的，怎么出现在那条里"。

**仍然没有覆盖到的**：授权请求的端到端路由（要让替身经 `opts.Permissions.AuthorizeTool` 抬起一个真的 ask 决策，牵涉工具分类与权限模式）。寻址层已验（`TestCommandsAddressTheGivenSession`），管理器层 `Manager.ResolvePermission(id, …)` 按 id 查会话是引擎自己的测试范围。这条留给 P2 的手工验收：后台会话弹授权 → 角标 → 切过去 → 解决。

预览面板、输入草稿的每会话化挪到 P2：它们是"看得见哪条"的界面态，只有 UI 能同时开两条会话时才有意义。

### P2 — 会话列表 UI（并行开始可用）✅ 已完成

**后端**：`buildSessionHeld`（建一条会话）与 `closeCurrentHeld`（关掉当前那条）彻底分家——"先关掉旧的"是**替换式打开**的策略，不是"开一条会话"本身的一部分。新增命令：

| 命令 | 作用 |
| --- | --- |
| `OpenSession` | 在当前工作区**再开**一条，不动已开的 |
| `FocusSession(id)` | 切聚焦，并把工作区一并搬过去 |
| `CloseSession(id)` | 关一条（空串 = 聚焦那条），聚焦顺势落到幸存者 |
| `OpenSessions` | 列出此刻开着的（是谁、在哪、跑没跑、哪条聚焦） |
| `CloseAllSessions` | 退出时收干净 |

**一个不变量收进了一处**：`focused` 与 `workspace` 必须同进同退——没带 id 的命令打给 `focused`，而工作区寻址的那批（文件列表、技能、子代理、MCP、记忆）读 `workspace`。原先三处各自设置。现在统一走 `focusLocked(entry)`。错开的表现是"切了会话，文件浏览器还停在上一条的目录上"——不报错，只是不对。

**退出钩子也改了**：`main.go` 原来调 `CloseSession()`，多会话下只关一条，剩下的会带着未收尾的回合被进程带走。

**前端**：
- `useSession` 管"开着哪几条"。**后端是唯一权威**，每次生命周期动作后回读——前端自己记账迟早对不上（替换式打开会关掉当前那条再开一条，失败的打开又不留痕迹）。
- 侧栏多了「打开中」一栏：运行指示（复用既有的 `blip`，没新造动画）、**待审批角标**、关闭按钮。角标**不随 hover 隐藏**。
- 状态条上一个不会被折叠收起的全局入口：「N 个会话在等你」，点它跳到最早在等的那条。侧栏可以折叠，只有角标是不够的。
- 「新建对话」在已有会话时变成**加开一条**。
- 输入草稿、预览标签按会话存。

**预览标签必须分开，这是多会话引入的一个真 bug**：diff 标签里存的是 `snapshotId`，而复审是拿它去问**聚焦会话**的编辑存储。共用一份的话，从 A 打开的 diff 在切到 B 之后会静默失效——面板还在，内容变成"找不到"，没有任何东西提示你那属于另一条对话。宽度、自动预览开关这类纯界面偏好仍然共用。

**验收**（已通过）：`go test -race ./internal/...` 全绿；`golangci-lint` 0 issues；`protogen --check` 不漂移；前端 `typecheck` / `lint` / `vitest` 283 用例 / `build` 全绿；打包启动正常。

新增 Go 测试：`TestOpenSessionKeepsExistingOnesOpen`（加开时正在跑的那条不受影响）、`TestFocusSessionMovesWorkspace`、`TestCloseSessionOnlyClosesThatOne`、`TestCloseSessionRefocusesToASurvivor`。

**仍需手工验收**（自动化没覆盖到的那块）：开两条会话各发一个长任务，切来切去两边输出都不丢；其中一条触发授权，切走后角标与状态条入口出现，点回去能正确解决。这条同时也是 P1c 欠下的"授权端到端路由"。

#### 手工验收里冒出来的两个 bug（都已修，各带一个回归测试）

两个都是**「替换式打开」在多会话下的余震**：`StartSession` / `SwitchWorkspace` / `ResumeSession` / `NewSession` 会先关掉聚焦的那条再开新的。单会话时这个策略无处可错（同时只有一条会话），并行之后它每碰到一条"已经开着的会话"就出事。

**其一：发完消息点「新建对话」，刚发出去的回合当场停掉。** 侧栏按 `openList` 是否为空来二选一——空就走 `NewSession`（替换式），非空才走 `OpenSession`（加开）。而 `start()` 之后漏了一次 `refreshOpen()`，首个会话从没进过那份列表，于是"第一条会话"永远走成替换。修法不止是补回读：判据换成 `session.info`（同步、永远是当前值），不再让一个异步回读、天然滞后的列表决定要不要销毁用户正在进行的工作。`NewSession` 的 doc 注释也写明了它是替换式、会取消在跑的回合。

**其二：点「最近对话」里一条正开着的会话，报 `session already exists`，而且刚才聚焦的那条已经被白白关掉。** 「最近对话」（工作区里存着的）与「打开中」（此刻开着的）天然重合，点到重合的那条时，恢复路径先 `closeCurrentHeld()` 关掉聚焦的，再拿目标 id 去 `Manager.Create`——目标还在 manager 表里，撞 `host.ErrSessionExists`。用户看到的是一句莫名其妙的英文错误，外加丢了一条会话，目标也没打开。

修在两层，**后端那层是必需的**（前端只是省一趟往返，不是防线）：`openSessionWithConnectionHeld` 前置 `focusIfAlreadyOpenHeld` ——要恢复的会话已经开着且不是聚焦的那条，就只把聚焦切过去，不关旧的也不重建。**"是聚焦的那条"必须放行**：换模型、改设置、重载 MCP 全靠"关掉再按同一个 id 恢复"让新配置生效同时保住历史（`rebuildResumingWithConnectionHeld`、`ReloadMCPServers`），那条路不能拦。代价写在函数注释里：目标已开着时本次调用带的 cfg 变更不生效——比"报错并连坐关掉另一条"好得多，而且换模型有 `SetModel` 那条按会话生效的正路。

回归测试：`TestResumeAlreadyOpenSessionFocusesIt`（不报错、聚焦切过去、另一条还开着、而且是**原来那条**不是同 id 重建出来的）。

### P3 — 多工作区 ✅ 已完成

**`OpenSession(workspace)`**：空串 = 就在当前目录再开一条；给了目录就开在那个目录。目录经 `resolveWorkspaceDir` 归一成绝对路径并确认存在，失败时**什么都不动**（聚焦不变、会话表不变）——只有换了目录才写一次配置，把它记进最近工作区（MRU）。

**`SwitchWorkspace` 整个删掉了**，不是留着不用。它的语义是"关掉当前会话、在新目录重开一条"——换个目录看看就得丢掉手上正在跑的回合。多工作区并行之后这件事有了正解：`OpenSession(dir)` 加开一条，两个项目同时挂着；真要腾地方，`CloseSession` 是一个明确的动作。留一个没有调用方的命令只会烂掉（没有 UI、没有测试、下一个人还得猜它和 `OpenSession` 差在哪），所以连同 `CommandKinds` 里的登记一起删。搬家里唯一留下来的是 `resolveWorkspaceDir`。

界面上那颗工作区按钮的角标因此从「切换」改成「新开」，提示语写明"已开着的会话不受影响"——这颗按钮的后果变了，标签必须跟着变。

**后端其实早就准备好了**：每条会话的目录记在 `sessionEntry.workspace`，聚焦切换时 `focusLocked` 把 `a.workspace` 一并搬过去（P2 收的那个不变量），预览服务器按目录共享并计数（`previewRef`）。P3 在后端只是把入口开出来。

**前端两处必须跟着改，否则并行两个目录时会静默出错**：

- `usePreviewPanel.closeAll` 原先清的是**所有会话**的标签。那是"换工作区 = 关掉当前会话重开"时代的正确做法（标签存的是工作区相对路径与编辑快照 id，换了目录全成坏引用）。现在换目录是加开一条，留在旧目录的会话还开着、它们的标签依然有效，一把清空就是把别人的面板也清了。改成只清聚焦这条——它对应的本来就是面板上那颗「关闭全部」。会话与目录一一对应且终生不变，"标签失效"这件事从此只随会话一起消失（`dropSession`）。
- `useSession` 的 `onWorkspaceChanged` 回调**删掉了**。它唯一的作用就是上面那次 `closeAll`；而文件清单的重载本来就挂在 `session.info.sessionId` 的变化上（`App.tsx` 的 `reloadFiles` effect），切聚焦、加开、恢复三条路都覆盖到了，不需要第二个通知渠道。

**会话列表标目录**：「打开中」的行在**几条会话落在不同目录时**才显示目录名（同一个目录里全都一样，那是纯噪声）。这是多工作区下唯一能分清"两条同名会话分别属于哪个项目"的东西。

**验收**（已通过）：`go test ./internal/desktop` 全绿；`golangci-lint` 0 issues；`protogen --check` 不漂移；前端 `typecheck` / `lint` / `vitest` 290 用例全绿。

新增 Go 测试：`TestOpenSessionInAnotherWorkspace`（新会话开在指定目录、正在跑的那条不受影响、聚焦与工作区一起搬、第一条会话的目录不被改、预览服务器起在新目录上）、`TestOpenSessionRejectsMissingDirectory`（目录不存在时报错且什么都不动）。测试替身 `fakeHostSession` 顺带补上了 `Status().CWD`——不带它就测不出"开在哪个目录"。

#### 手工验收里冒出来的第三个 bug：`a.config.CWD` 会过期

**症状**：点「最近对话」里一条，对话变成空的，而且刚才那条正在跑的会话也没了。

**根因**是一个被多工作区打破的隐含前提。`a.config` 是进程级的"下一条会话用什么连接"，它带着 `CWD` 只因为它是上一次建会话时的**整份快照**。单工作区时 `a.config.CWD` 恒等于 `a.workspace`，所以谁都可以照抄；多工作区之后不再成立——在 dir2 开过一条会话，`a.config.CWD` 就是 dir2，而聚焦切回 dir1 的会话时它不会跟着回来。

于是从 dir1 的「最近对话」点一条：拿着 dir1 的会话 id 去 **dir2** 的存储里恢复，那里没有这条记录，引擎照这个 id 在 dir2 建了一条**空**对话（还在那边落了个同名文件），而 dir1 那条正跑着的会话已经被替换式打开关掉了。不报错，只是全错。

`SwitchModel`（换模型要原地重建会话）、`ReloadMCPServers` 有同一个毛病，表现更隐蔽：换一次模型就把这条会话**搬到另一个工作区**去了。

**修法是把它变成一条说得出口的规矩**，而不是四个调用点各记一次：

- `configForWorkspace(ws)` —— 取"下一条会话"的配置并**强制**目录。`a.config` 的字段注释直接写明"CWD 不可信，一律用它取"。`NewSession` / `ResumeSession` / `OpenSession` / `ReloadMCPServers` 全部改走它。
- `workspaceOfSession(id)` —— 目录是**会话的属性**。原地重建（`rebuildResumingWithConnectionHeld`）从被重建的那条会话身上取目录，不再看 `liveConfig`。

**前端还欠一半**：`focusOn` 之后没有重读「最近对话」。那份清单是按工作区列的，切到另一个目录的会话后侧栏还挂着上一个目录的清单——点它一条就又是"拿甲目录的 id 去乙目录恢复"。补了一次 `refreshRecents()`。

回归测试 `TestResumeUsesTheFocusedWorkspace`：在 dir2 开过会话（把 `a.config.CWD` 弄脏）→ 聚焦回 dir1 → 恢复历史 → 断言真正建出来的会话开在 dir1。**这条测试确认过是有牙的**：把 `ResumeSession` 改回 `cfg := a.config` 它立刻失败。

#### 收口：**所有"打开一条会话"的入口都改成加开**

同一个坑报障了三次——「新建对话」、「换工作区」、「点开一条最近对话」，每次都是"我发出去的活正跑着，点了个别的东西，它就没了"。前两次是各自修的；第三次说明该修的不是某个入口，而是那条**策略**。

于是 `addSessionHeld` 从 `openSessionWithConnectionHeld` 里拆出来——两者只差一句 `closeCurrentHeld`，而那一句就是"替换式打开"的全部含义。界面上三个入口（`OpenSession`、`OpenSession(workspace)`、`ResumeSession`）现在都走它：

> **没有任何一个动作会顺手销毁别的会话；关闭只有 `CloseSession` 一个明确入口。**

替换式打开只留给真正意味着"这条会话要被换掉"的地方：换模型/改设置的原地重建（同一个 id 重建并恢复历史）、重载 MCP。`NewSession` 留着那个语义（"换一条干净的，旧的不要了"），但界面已经不走它。

代价是会话会攒起来：每点开一条历史对话就多一条活着的会话，各自占着 MCP 连接与存储句柄。这是**可见且可撤销**的——都列在「打开中」里，关掉是一个明确的动作；而"悄悄弄丢正在跑的活"既不可见也不可撤销。硬上限留给 P4 的 `host.Limits.MaxSessions`。

回归测试 `TestResumeKeepsRunningSessionOpen`：一条会话的回合正在跑 → 恢复另一条历史对话 → 原来那条仍在跑，两条都开着。

#### 「打开中」里切一下也会关掉会话——两个叠在一起的界面缺陷

后端先被排除了：`TestFocusAndListDoNotStopARunningTurn` 把界面切换时实际发生的那几个调用（`FocusSession` + `Status` + `ListSessions`，来回两次）按顺序走一遍，回合照跑、两条会话都在。既然这一层是干净的，问题只能在上面。

**其一：列表顺序是随机的。** `OpenSessions()` 直接遍历 `a.sessions`（一个 Go map），而 map 遍历顺序**每次都不一样**。于是界面每回读一次列表，行就重新洗一次牌：用户点一行 → `refreshOpen()` 回读 → 行跳位 → 再点同一个位置就点到了别人身上。这一栏每行右侧就是关闭按钮，代价是关掉一条正在跑的会话，而且没有任何报错。

修法是给 `sessionEntry` 加一个登记序号 `seq`（`registerSessionLocked` 发放，只增不减），列表按它排——语义就是"打开的先后"。回归测试 `TestOpenSessionsIsStablyOrdered` 连读 20 遍比对顺序；**确认有牙**：把 `sort.Slice` 去掉，第 0 遍就失败。

**其二：关闭按钮悬停才显形。** `hidden group-hover:inline-flex` 意味着指针一移到行上，✕ 就**凭空出现在指针底下**——"想切过去"点成了"关掉"。改成常驻（暗一点的 `text-faint/70`），并且**关闭正在跑的会话要多问一句**（复用侧栏已有的 ConfirmDialog）。常驻的代价是行里多一个字符，换来的是这颗按钮只会被瞄准了才点到。

两条合起来是同一个教训：**破坏性操作不能靠"用户不会点到那里"来防**——尤其当它的位置本身在动。

#### 关掉最后一条会话会被扔回首屏

`closeOne` 在关完发现一条都不剩时做的是 `setStarted(false)`——界面整个退回起始页：重挂载、重跑登录门与工作区选择、清掉屏幕上的一切。而用户做的只是"把手上这条对话收掉"，工作区、连接、模型一样都没变，凭什么要他从头再来一遍。

改成**开一条新的空会话并留在对话界面**，和浏览器关掉最后一个标签页给你一个新标签页是同一个道理。只有连新会话都开不出来（没有工作区或没有可用模型，比如还没真正启动过）才回首屏。

后端那半本来就成立，加了 `TestClosingTheLastSessionKeepsTheWorkspace` 把它钉住：关掉最后一条不会顺手清掉 `a.workspace`，所以紧接着的 `OpenSession("")` 仍然开在原来的目录里。

**仍需手工验收**：两个不同目录各开一条会话同时跑，切来切去时文件浏览器、`#` 引用候选、预览标签、「最近对话」各自指向自己的目录，互不干扰。

### P4 —（可选）资源与稳定性收口

- 给 `host.Limits.MaxSessions` 设一个上限，超出时明确报错而不是无声耗尽。
- 后台会话失败的可见性（列表红点 + 汇总）。

---

## 五、风险与对策

| 风险 | 说明 | 对策 |
| --- | --- | --- |
| **同工作区并发写同一文件** | 两个会话同时改一个文件会互相覆盖，「已编辑」的撤销栈也会打架。这是并行**固有**的风险，不是实现缺陷 | 至少要让用户看见：同一工作区有多个会话在跑时，在列表上标出来。更强的方案（文件级冲突检测）留后续 |
| **审批看不见** | 后台会话卡在等授权，用户以为在跑 | §3.6 的角标 + 全局入口，P2 必做 |
| **审批解错会话** | 全局队列 + `currentID` 解析 | `ResolvePermission` 带 `sessionId`，P0/P1 必做 |
| **并发事故** | 新增第三层锁 | 锁序写进注释；`-race` 跑并行会话的测试 |
| **界面发卡** | 多路流式输出同时触发重渲染 | 非聚焦会话的更新不进渲染路径；合批 |
| **连接重建打断别人** | 切租户会重建 manager 连接 | §3.5 的闸门：有会话在跑就拒绝，并说明原因 |
| **资源耗尽** | 每工作区一个预览服务器 + 文件索引 + MCP 连接 | 预览服务器引用计数；`MaxSessions` 上限 |
| **录音卡片串台** | 录音是进程单例，卡片属于某个会话 | §3.7 记录归属会话 |

---

## 六、明确不做（本期）

- **每会话独立连接**（不同 provider / 租户同时在线）。要做的话得把 `liveConfig` 那一整套拆成每会话一份，并处理 manager 层的连接池，工作量与风险都高于本期其余部分之和。当前的替代做法：模型可以每会话不同（引擎已支持），连接统一。
- **文件级冲突检测**。同工作区并行写文件的冲突先靠"可见"而不是"阻止"来管理。
- **会话间的资源配额**（每会话的 token/时长上限）。
- **跨进程并行**。单实例锁保持不变（`main.go` 的 `brandID`），并行发生在一个进程内。

---

## 七、工作量与顺序建议

按上面的分期，P0 与 P1 是地基且互相独立可验证，建议**一期一次合入**，不要攒成一个大改动——`currentID` 有 25 个引用点，混着前端一起改会让 review 和回归都失去着力点。

P2 之后并行才对用户可见；在此之前的每一期，验收标准都是"行为与改动前一致"。
