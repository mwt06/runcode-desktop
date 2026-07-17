# engine 模块公开 API 与稳定性契约

`github.com/wt68/runcode/engine` 是三端（CLI/TUI、桌面、未来服务端）共用的会话引擎。本文档定义其公开面、兼容规则、会话状态的可序列化边界，以及服务端外壳的预留设计。配套阅读：[engine/README.md](../engine/README.md)（**使用指南**：从 Build 到 RunTurn 的消费者视角）、[docs/architecture.md](./architecture.md)（模块边界）、[docs/protocol.md](./protocol.md)（双端 wire 协议）。

## 1. 公开面（协议 + 服务面）

- **入口**：`engine.Build(cfg Config, opts Options) (*Session, error)`——唯一装配入口。典型消费者 import ≤6 个包：`engine` + 按需 `engine/llm`（消息/图片）、`engine/turn`（RunTurn 返回值）、`engine/permissions`（实现 Approver）、`engine/tool`（事件通道/ExtraTools）、`engine/sessions`（会话列表/恢复）。
- **协议类型**（服务端序列化基准，变更受 §2 约束）：`turn.Result`、`turn.ToolDescriptor`、`turn.EditRecorder/EditHandle`、`tool.Event/OutputLine/FileReference/ResultImage`、`llm.Message/ImageSource`、`permissions.ApprovalRequest/ApprovalResponse/ApprovalSummary`、`sessions.Info/SessionMeta`、`engine.Status`。
- **端口**（引擎定义、外壳注入，nil = 本地默认零感知）：

| 端口 | 本地默认 | 服务端实现（后续轮） |
|---|---|---|
| `Options.Backend`（sessions.Backend v2） | Build 按 Config 自开 jsonl/sqlite | Redis 热层 + DB 归档装饰器（契约见 sessions.Backend 文档；验收 = `sessions/backendtest.Run`） |
| `Options.SubagentLimiter` | 仅每会话上限 8 | 跨会话全局预算 |
| `Options.ShellBudget`（bash.Budget） | 仅每会话上限 16 | 跨会话全局预算 |
| `Config.WebProxy` | ""（env 回退） | 每会话代理 |
| `Config.ToolEnv` | 空（仅继承进程 env） | 每会话 HOME/凭证/代理隔离 |
| `Config.TokenSource`/`OnUnauthorized` | 外壳令牌管理器 | 多用户令牌服务（须 goroutine-safe、刷新单飞） |
| `Options.Permissions`/`Approver` | safe 模式 | 外壳的异步审批桥 |
| `Options.EditRecorder`（turn.EditRecorder） | nil（不捕获） | 宿主编辑捕获 |
| `Options.ToolRuntime`（**登记中，未实现**） | 进程内内置工具 | 工具网关沙盒客户端 |

多会话宿主层 `engine/host`（根模块）在这些端口之上提供会话表/信封/配额/审批路由/后端池；词汇稳定并被第二个宿主检验后可升格为 engine 公开包。

## 2. Config/Options 兼容规则（PR 评审依据）

1. **只增不改不删**：新字段零值必须等价旧行为（"0/nil/空 = engine 默认"约定延续）；字段语义永不复用。
2. 废弃流程：doc comment 标 `Deprecated:`，至少保留一个 minor 版本。
3. 行为注入统一走 Options 的接口/函数字段；新能力 = 新可选字段，构造函数签名永不膨胀。
4. 协议类型变更同受规则 1 约束（它们是服务端序列化基准）。
5. 版本：嵌套 tag `engine/vX.Y.Z`；v0 期间允许带 CHANGELOG 的破坏性 minor；服务端消费方出现后升 v1。
6. **依赖铁律**：engine 永不依赖根模块（CI 审计强制）；engine go.mod 永不出现 Redis/网关等远程基础设施依赖——端口在引擎、实现在外壳。

## 3. 会话状态三分法（横向扩展的地基）

每份会话内存态必须可归入以下三类之一；归不进去的就是扩展障碍，必须消除或补持久化：

**(a) 可从 Config 重建**（无需迁移）：provider/model 接线、工具集、MCP 连接、skills/agents 目录、系统提示、权限服务结构。

**(b) 已持久化**（跨进程/跨节点随会话走）：
- 消息历史：`sessions.Backend`（`Store.Append` 每 turn 原子提交；`LoadHistory` 读全量——Close 同步落盘 + Resume 读全量 = 节点 A 关、节点 B 续，无需额外 flush 协议）；
- `sessions.SessionMeta`（model/permissionMode/planMode/thinkingEffort/reasoningScenario）：运行时开关随会话迁移——planMode 丢失最危险（只读会话静默变可写），故 setter 即存、Resume 即读；
- 标题 sidecar；项目级允许规则/denylist（workspace 内 permissions.json，原子 rename，天然随共享存储迁移）。

**(c) 声明可丢弃**（跨节点 resume 后安全降级，方向全部朝更严格/更安全）：
- read-set：丢失 → Write/Edit 的读后写门挡下首写，模型重读即恢复；
- session 作用域允许规则、harm 熔断计数：丢失 → 回到更严格状态；
- analyzeDone、in-flight turn：丢失即重来（历史以 turn 为持久化粒度，崩溃丢当前 turn）；
- 后台 shell 与 MCP 子进程：**节点本地物不可迁移**；resume 后查询旧 id 得到清晰报错（"unknown background shell"）。

**节点亲和不变式**：turn 内节点亲和（goroutine/子进程本地）；turn 间会话可由任意节点经 LoadHistory+LoadMeta 重建。热层 write-through 的远程 Backend 使 sticky/非 sticky 路由都只是性能选择，不是正确性问题。

## 4. 工作区文件触点清单（服务端拓扑 (ii) 的待抽端口，本轮只登记）

若服务端选择「中心引擎 + 工具网关」拓扑（引擎节点不挂载工作区文件系统），除 `Options.ToolRuntime` 外还需抽象以下引擎内的直接文件访问；选择「沙盒即会话」拓扑（每会话引擎跑在沙盒内）则全部不需要：

| 触点 | 位置 | 现状 |
|---|---|---|
| 项目上下文加载（RUNCODE.md/CLAUDE.md 向上查找） | engine/projectctx | Build 时直读 |
| skills/agents/命令目录发现 | engine/skill、engine/agent（discovery） | Build/Reload 时直读 |
| 项目级允许规则持久化 | engine/permissions（workspace/.runcode/permissions.json） | 直读写（原子 rename） |
| 会话历史/标题/meta（jsonl/sqlite 本地后端） | engine/sessions | 经 Backend 端口 ✔（换远程实现即可） |
| 项目记忆 | engine/memory（workspace/.runcode/memory.md） | 直读写（进程级 Shared 锁） |
| 附件读取 | 外壳（desktop attachments） | 外壳职责 |

## 5. 服务端外壳预留设计（不实施）

- 形态：同仓嵌套 module `cmd/runcode-server`，复刻 desktop 外壳模式——require engine + import `engine/host`（Go internal 前缀规则允许同仓兄弟模块）+ `engine/protocol`。
- 接线：`host.Sink` 实现为 WebSocket/SSE 发射器（信封原样推送，单连接多路复用）；命令面 = `POST /api/v1/rpc/{Command}` 或 WS RPC，会话寻址进 URL/RPC 信封（见 docs/protocol.md §7）；审批经 WS 往返 `ResolvePermission`。
- 会话状态：注入 Redis 热层 Backend + DB 归档装饰器（契约在 sessions.Backend 文档；`backendtest.Run` 是验收标准）；跨节点互斥由路由层保证，引擎不做分布式锁——如需租约，以类型断言探测的可选接口扩展（`Lease(id, owner, ttl)`），不进主接口。
- 事件重放明确不做：信封 seq 只用于缺口检测，重连走 Status+ResumeSession 对账。
- 部署建议：每会话独立 workspace（同库并发与共享 memory.md 的问题天然消失；host 的 backendPool 仅在共享 workspace 时才承担正确性职责）。
- 多用户凭证：per-session `Config.TokenSource` 注入（契约见 config.go 注释）；引擎与组织/部署形态无关。

## 6. 明确不做（防扩张，与批准方案一致）

MCP 跨会话共享（per-session 连接是正确的安全隔离）；memory.md 跨进程文件锁；分布式会话锁/租约实现；事件重放/outbox；服务端外壳本体；多租户令牌管理；协议独立 schema 工具链；Redis/DB Backend 与工具网关实现（端口+契约测试已备）。
