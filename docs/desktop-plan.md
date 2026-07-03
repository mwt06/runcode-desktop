# runcode 桌面版方案（Desktop / Wails）

> 状态：方案稿（待评审通过后进入 P0 实现）。
> 技术栈：**Wails v2** + Web 前端。运行形态：**同进程内嵌**，但引擎边界做成**传输无关（transport-agnostic）**，未来可零成本切换为独立 daemon。

## 1. 设计目标与原则

桌面版不是另写一个产品，而是给现有 `runcode` 引擎加上**第三个前端**（CLI、TUI 之后）。遵循 `CLAUDE.md`：优先可靠、可扩展、高内聚、低耦合，不为少改代码牺牲长期架构。

核心约束：**引擎（`internal/repl.Session` 及其装配）零侵入或仅做兼容性扩展，绝不为 GUI 在引擎内写 UI 逻辑。**

## 2. 现状复用盘点

引擎与 UI 已彻底解耦，三个「插座」即全部交互面：

| 插座 | 类型 | 现有接法 | 桌面版接法 |
|------|------|----------|------------|
| 助手流式文本 | `SessionOptions.StreamDelta func(string)` | stdout / Bubble Tea | 转 EngineEvent → 前端事件 |
| 工具生命周期 | `SessionOptions.ToolEvents chan<- tool.Event` | TUI 工具卡片 | 转 EngineEvent → 前端事件 |
| 权限审批 | `permissions.Authorizer` / `Approver` | stderr / TUI 模态框 | **异步**：发审批请求事件，阻塞等前端回传 |

直接复用、无需改动：permissions、tools、Provider（Anthropic/OpenAI 兼容）、MCP、skills、sub-agents、memory、session 持久化（JSONL/SQLite）、compaction、telemetry、transcript、settings(TOML)。

## 3. 关键结构问题（P0 必须先解决）

把配置组装成可运行 Session 的全部接线逻辑 `newSessionForConfig`（约 160 行）目前在 `cmd/runcode` 的 `package main` 中，**外部无法 import**。桌面版若抄一份，就产生两套并行接线——正是要避免的重复逻辑与抽象泄漏。

> **P0 基础层：抽出 `internal/engine` 引擎门面包**，把 session 构造、MCP/skills/agents/memory 装配、资源生命周期收敛进去。CLI、TUI、桌面版三者共用。这一步同时提升 CLI/TUI 内聚度，是一鱼三吃的基础投资。

## 4. 架构核心：传输无关的 Session Host

这是回应「要最优解、不牺牲扩展性」的关键设计。不在「内嵌 vs daemon」里二选一，而是**把引擎边界定义成一套纯数据的协议**，让同一套门面既能被 Wails 同进程直接调用，也能日后包一层 JSON-RPC/WebSocket 变成 daemon——切换只换传输层，业务零改动。

```
            ┌─────────────────────────────────────────────┐
            │              前端 (Web, Wails)                │
            │  会话视图 / 工具卡片 / diff / 文件树 / 设置     │
            └───────────────┬───────────────▲──────────────┘
              Command(纯数据) │               │ EngineEvent(纯数据)
            ┌───────────────▼───────────────┴──────────────┐
            │   传输层（P1 用 Wails 直接绑定；可换 RPC）      │
            └───────────────┬───────────────▲──────────────┘
            ┌───────────────▼───────────────┴──────────────┐
            │      internal/engine  (Session Host 门面)      │
            │  OpenSession / Send / Interrupt / Compact ...  │
            │  EventStream(typed) + Permission 异步协议       │
            └───────────────┬───────────────────────────────┘
            ┌───────────────▼───────────────────────────────┐
            │   现有内核 internal/repl.Session + 全部子系统    │
            └────────────────────────────────────────────────┘
```

### 4.1 门面 API（草案，纯数据 in/out，不含任何 UI 类型）

```go
package engine

// Host 管理一个或多个会话的生命周期，是传输无关的引擎门面。
type Host interface {
    OpenSession(ctx, OpenRequest) (SessionID, error)
    Send(ctx, SessionID, SendRequest) error          // 异步：结果走 Events
    Interrupt(SessionID) error                        // 取消当前 turn
    ResolvePermission(SessionID, reqID, Decision) error
    Compact(ctx, SessionID) (before, after int, error)
    SwitchModel(SessionID, model string) error
    ListSessions(workspace string) ([]SessionMeta, error)
    Events() <-chan Event                             // 全部会话的统一事件流
    CloseSession(ctx, SessionID, reason string) error
}
```

### 4.2 事件模型（引擎 → 前端，typed，纯数据）

`AssistantDelta` / `ThinkingDelta` / `ToolStarted` / `ToolOutput` / `ToolCompleted` / `PermissionRequest` / `TurnEnd` / `UsageUpdated` / `Error` / `SessionEnded`。
由 `StreamDelta` 回调与 `ToolEvents` 通道适配而来，统一成一个出口。

### 4.3 异步权限协议（唯一有并发设计含量的点）

实现一个 `engine.asyncApprover`，满足现有 `permissions.Approver` 接口：

1. 引擎调用 `Approve(action)` 时，生成 `reqID`，发 `PermissionRequest` 事件，**阻塞**在一个 per-request channel 上。
2. 前端弹窗，用户点 allow-once/session/project/deny，前端回调 `ResolvePermission(sid, reqID, decision)`。
3. Host 把 decision 投递到对应 channel，`Approve` 返回。
4. 必须正确处理：turn 被 `Interrupt`/`ctx` 取消、超时、会话关闭时唤醒所有挂起请求并按 deny 兜底。

此机制与传输无关——同进程或远程 daemon 行为一致，复用同一份代码。

## 5. 仓库与工程结构

```
internal/engine/            ★P0 新增：传输无关引擎门面（Host、Event、asyncApprover、装配）
cmd/runcode/                现有 CLI/TUI，改为调用 internal/engine（去重）
cmd/runcode-desktop/        ★新增：Wails 应用入口（Go 后端 = engine 适配 + 绑定）
desktop/                    ★新增：Web 前端（React/Svelte + Vite）
  src/views/                会话、工具卡片、diff、文件树、设置
  src/bridge/               Wails 事件订阅 + 命令调用封装
wails.json                  ★Wails 工程配置
docs/desktop-plan.md        本文件
```

前端技术建议：React + Vite + TypeScript；Markdown 用 react-markdown + remark-gfm，语法高亮用 shiki，diff 用 diff2html/CodeMirror merge view。这些正好补上 TUI 缺的 diff viewer / 文件树 / 语法高亮。

## 6. 分阶段计划（目标：直接做到产品化 P2）

### P0 — 基础层（纯重构，行为不变，测试兜底）✅ 已完成

已落地 `internal/engine` 引擎门面：

- **`Config`**（`config.go`）：会话的已解析配置；CLI 经 `type chatConfig = engine.Config` 别名零改动复用。
- **`Build(cfg, Options)` + `Session`**（`build.go` / `engine.go`）：迁移原 `cmd/runcode` 中 `newSessionForConfig` 的全部装配逻辑（provider、tools、permissions、MCP、skills、sub-agents、memory、persistence、prompt），并提供传输无关的 `Session` 宿主（`RunTurn`/`RunTurnWithImages`/`ResetHistory`/`Compact`/`SetPermissionMode`/`SetModel`/`Status`/`Close`）与 `Resources`。
- **`Options`**：纯数据/接口注入边界——`StreamDelta func(string)`、`ToolEvents chan<- tool.Event`、`Permissions *permissions.Service`、`Approver permissions.Approver`、`Warn` / `TelemetryWriter`。这是桌面前端的接入点。
- **`discovery.go`**：skills/agents/memory 约定目录发现（`LoadSkills`/`LoadAgents`/`MemoryStore` + 常量），原 cmd/runcode 测试一并迁入 `discovery_test.go`。
- CLI（`defaultChatRunner`）与 TUI（`tuiSessionService`）改为消费 `internal/engine`，删除全部重复接线。
- 验证：`go build ./...`、`go vet ./...`、`go test ./...` 全绿；`-race` 在 engine 及其消费方（cmd/runcode、repl、ui）全绿；CLI/TUI 行为零回归。

> 桌面前端接入点：调用 `engine.Build(cfg, engine.Options{...})` 拿到 `*engine.Session`；注入一个实现 `permissions.Approver` 的异步审批器（仿 `internal/ui.Approver`，把请求桥接到 Wails 弹窗）、一个把 `StreamDelta`/`ToolEvents` 转成 Wails 事件的适配层。

### P1 — 桌面最小骨架（打通「引擎=第三前端」）✅ 已完成

- **`internal/desktop`**（同模块、不依赖 Wails、已单测+`-race`）：传输无关的桌面核心。
  - `App`：单会话管理器，异步跑 turn，把 `StreamDelta`/`ToolEvents`/警告转成扁平事件流（`assistant:delta` / `tool:event` / `permission:request` / `turn:end` / `turn:error` / `warning`），命令方法（StartSession/SendMessage/Interrupt/ResolvePermission/SetPermissionMode/SetModel/Compact/Reset/Status/CloseSession）不阻塞 UI 线程。
  - `Approver`：异步审批器，实现 `permissions.Approver`，发审批事件→阻塞等前端 `ResolvePermission`，中断/关闭时 `DenyAll` 兜底，绝不泄漏 goroutine（已覆盖 resolve/cancel/deny-all/未知 id 测试）。
  - `config.go`：`StartSessionRequest` → `engine.Config`，缺省回退环境变量。
- **`cmd/runcode-desktop`**（嵌套 Go module，隔离 Wails 重依赖，根模块 `go build ./...` 不受影响）：Wails 外壳 `main.go`（事件 sink + `Bind(app)` + `//go:embed frontend/dist`）。
- **`frontend/`**（React + Vite + TS）：开始表单（选工作区/模型/provider/权限模式）、流式对话、交错的工具卡片、异步权限弹窗、状态栏（模型/模式切换/token/估价）、`/compact` `/clear`。
- 验证：根模块 `go build ./...` / `go vet ./...` 全绿（嵌套模块自动排除）；`internal/desktop` `-race` 全绿；嵌套模块 `go build ./...` 对真实 Wails v2.12.0 编译通过；`frontend` `npm run build` 通过。
- 构建/运行说明见 `cmd/runcode-desktop/README.md`（`wails dev` / `wails build`）。

> 已具备产品化基础，下一步 P2 在此之上加：session 历史侧栏、Markdown+语法高亮、Edit/Write side-by-side diff viewer、文件树、设置面板（接 TOML）、图片输入。

### P2 — 产品化（本次目标终点）
- 会话历史侧栏：列表 / 恢复（复用 sessions Backend）/ `/clear` / `/compact` / `/cost`。
- 富渲染：Markdown + 语法高亮；Edit/Write 的 side-by-side diff viewer。
- 工作区文件树 + 文件预览。
- 设置面板：provider/model/key、权限模式、MCP servers、hooks、skills/agents 一览（读，复用现有 loader）。
- 图片拖拽/粘贴输入（复用 `RunTurnWithImages` 与 attachments）。
- 中断按钮、token/成本实时显示、错误友好提示。

### P3 — 进阶（暂不做，列为后续）
多窗口/多工作区、托盘常驻、自动更新、MCP/hooks 可视化编辑、daemon 远程模式（届时只在传输层包 RPC，门面不动）。

## 7. 取舍说明

**现在必须做（可靠性/扩展性基础）：**
- `internal/engine` 门面——消灭接线重复，三前端单一来源。
- 传输无关的 Host + 事件协议——满足「不牺牲扩展性」，daemon 化是未来换传输层而非重写。
- 异步审批器——GUI 权限地基，做不对会脆弱（挂起请求、取消、超时必须严谨）。

**暂不做（避免过早复杂化）：**
- P1/P2 运行形态就用 Wails 同进程内嵌，不引入真实进程边界与 RPC 协议——但因边界已是纯数据协议，未来切 daemon 不返工。
- 多窗口/多工作区、插件 UI、自动更新放到 P3。

## 8. 风险与对策
- **CGo/构建**：Wails 在 Windows 需 WebView2 运行时（Win11 自带）；SQLite 用的是纯 Go `modernc.org/sqlite`，无 CGo 负担。
- **长 turn 阻塞 UI**：所有引擎调用异步化，事件流驱动渲染，前端永不同步等待。
- **权限并发**：审批挂起请求集中在 Host 管理，会话关闭/中断统一唤醒兜底 deny，并补单元测试。
- **重构回归**：P0 以现有测试矩阵为安全网，先重构后加功能。
```
