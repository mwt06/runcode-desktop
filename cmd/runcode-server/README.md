# runcode-server —— 服务端交接骨架

一个**只依赖 engine 模块公开面（`gitlab.ouc-online.com.cn/aibase/agentloop/...`）与标准库**的
HTTP 参考宿主：命令面 RPC + 事件面 SSE，会话托管全部交给 agentloop 的 `host.Manager`。
它的存在意义是向独立仓库的服务端同事证明：**engine 的交接面是完整的**——拷走本目录、
改两行 go.mod 即可起步，不需要 runcode 根模块的任何内部包。

- 协议语义（回合状态机 / 信封 / 错误码 / 传输映射）：见 agentloop 仓库（同级
  `../agentloop`）的 `docs/protocol.md`。
- 依赖约束由测试固化：`TestOnlyEnginePublicImports`（deps_test.go）审计依赖闭包，
  只许 agentloop 与本模块自身，出现任何 `wt68/runcode` 包即红。第三方依赖为零（SSE 用 stdlib 即可，
  WS 升级留给正式服务端）。

## 快速开始

```bash
# 在本仓库内（go.work 已收编本模块）
cd cmd/runcode-server

# 跑测试（不需要 LLM 凭证——测试注入 fake 会话）
go test -race ./...

# 起服务（真实 LLM）
RUNCODE_PROVIDER=anthropic \
RUNCODE_MODEL=claude-sonnet-4-5 \
RUNCODE_API_KEY=sk-... \
RUNCODE_TOKEN=devtoken \
go run .

# 冒烟（另开终端）
TOKEN=devtoken ./scripts/smoke.sh
```

### 配置（flag > env > 默认值）

| 环境变量 | flag | 默认 | 说明 |
|---|---|---|---|
| `RUNCODE_ADDR` | `-addr` | `:8787` | 监听地址 |
| `RUNCODE_TOKEN` | `-token` | 空 | Bearer 令牌；**空 = 不鉴权**（启动时打印警告，仅限本机调试） |
| `RUNCODE_WORKSPACE_ROOT` | `-workspace-root` | `./workspaces` | 每会话 workspace 的父目录；StartSession 只接受其下的目录 |
| `RUNCODE_PROVIDER` | `-provider` | 空 | LLM provider（`anthropic`/`openai`） |
| `RUNCODE_MODEL` | `-model` | 空 | 模型名 |
| `RUNCODE_BASE_URL` | `-base-url` | 空 | provider Base URL 覆盖 |
| `RUNCODE_API_KEY` | `-api-key` | 空 | provider API Key |
| `RUNCODE_MAX_SESSIONS` | `-max-sessions` | 16 | 并存会话上限（0 = 不限），映射 `host.Limits.MaxSessions` |
| `RUNCODE_MAX_TURNS` | `-max-turns` | 4 | 并发回合上限（0 = 不限），映射 `host.Limits.MaxConcurrentTurns`；超限回合先收到 `turn:queued` |

## API 一览

传输映射对应 agentloop `docs/protocol.md` §7 的「HTTP/WS（未来服务端）」列，WS 降级为 SSE（协议明确允许）。

### 命令面：`POST /api/v1/rpc/{command}`

- body = JSON 请求；成功 = `200`（`SendMessage` 为 `202`：受理即返回，结果走事件面）；
  失败 = HTTP 4xx/5xx + `protocol.Error{code, message}` JSON。
- `kind == query` 的命令（`protocol.CommandKinds`）同时允许 `GET`，参数放 query string
  （如 `GET /api/v1/rpc/Status?sessionId=...`）。
- 错误码 → HTTP 状态：`no_session`/`not_found`→404，`busy`→409，`invalid_argument`→400，
  `not_logged_in`→401，`unavailable`→501，`internal`→500。

已实现的核心子集（其余 `CommandKinds` 命令统一回 `501 unavailable`，
message = `not implemented in the skeleton`）：

| 命令 | 方法 | 请求 body | 成功响应 |
|---|---|---|---|
| `GetProtocolInfo` | GET/POST | —— | `protocol.Info{protocolVersion, appVersion}` |
| `StartSession` | POST | `{workspace, systemPromptAppend?}` | `200 {sessionId, status}`（status = `protocol.SessionInfo`） |
| `SendMessage` | POST | `{sessionId, text}` | `202 {sessionId, accepted}`；结果走 SSE（`turn:end`/`turn:error`）；回合中重复提交 → `409 busy` |
| `Interrupt` | POST | `{sessionId}` | `{ok}`；取消在跑/排队回合，未决审批按 deny 解除 |
| `CloseSession` | POST | `{sessionId}` | `{ok}`；幂等，同时断开该会话全部 SSE 订阅 |
| `ResolvePermission` | POST | `{sessionId, requestId, decision}` | `{ok}`；decision ∈ `protocol.Decision*`（allow-once/allow-session/allow-project/deny） |
| `Status` | GET/POST | `{sessionId}` | `protocol.SessionInfo` |
| `ListSessions` | GET/POST | —— | `{sessions:[{sessionId, status?}]}`（仅活动会话，见 HANDOFF(session-list)） |

`workspace` 取值：workspace-root 下的**子目录名**（不存在则创建；`filepath.IsLocal`
校验，拒绝 `..` 等逃逸），或位于 root 之下的**绝对路径**。

### 事件面：`GET /api/v1/sessions/{id}/events`（SSE）

- `text/event-stream`，每条事件一帧 `data: <protocol.Envelope JSON>`；
  帧顺序即每会话 `seq` 顺序（`seq` 从 1 严格递增，见 agentloop `docs/protocol.md` §3）。
- 每订阅者有界缓冲 256 条；客户端消费不动即被断开并记日志——重连后凭 seq 缺口走
  `Status`（+ 将来的 `ResumeSession`）对账，事件**不重放**。
- `?after=<seq>` 被接受但忽略（骨架不做重放，见代码 HANDOFF(replay) 注释）。
- 回合状态机（agentloop `docs/protocol.md` §2）：`SendMessage` 受理后 0..n 个
  `assistant:delta | assistant:thinking | tool:event | permission:request | warning`，
  以恰好一个 `turn:end` 或 `turn:error` 收尾。

## 测试

```bash
go test -race ./...      # RPC 往返 / SSE seq / busy→409 / 鉴权 / 分发表完备性 / 依赖审计
go vet ./...
```

- 测试通过 `host.Options.Build` 注入 fake 会话（`fakes_test.go`），不需要 LLM 凭证。
- `TestDispatchTableMatchesCommandKinds`：分发表键必须都在 `protocol.CommandKinds` 登记。
- `TestOnlyEnginePublicImports`：依赖闭包与直接 import 双重审计（见文件头注释）。

## 交接指引（拷贝到独立仓库）

1. 整目录拷贝 `cmd/runcode-server/` 到你们仓库根（连同 `scripts/`、测试）。
2. 改 `go.mod` 的 module 名（例如 `github.com/yourorg/runcode-server`）。
3. `require gitlab.ouc-online.com.cn/aibase/agentloop vX.Y.Z` 固定到已发布 tag（本骨架已无 replace），
   构建环境设 `GOPRIVATE=gitlab.ouc-online.com.cn`；`go mod tidy`。
4. `go test -race ./...` 全绿即接手成功（依赖审计测试会持续守住交接面）。

### HANDOFF 锚点清单（代码内以 `HANDOFF(<名>)` 注释标出，即「同事要换掉」的位置）

| 锚点 | 位置 | 要做的事 |
|---|---|---|
| `HANDOFF(module)` | `go.mod` | 改 module 名；require 固定 tag + `GOPRIVATE` 直连内网 GitLab |
| `HANDOFF(config)` | `main.go` | env/flag → 你们的配置中心/密钥管理（`config` 结构体是唯一消费面） |
| `HANDOFF(auth)` | `server.go`（呼应 `main.go`） | 单令牌 Bearer → 多用户认证；把用户身份放进 request context，为每用户注入 `engine.Config.TokenSource`/`OnUnauthorized`（契约见 agentloop 根包 `config.go`：goroutine-safe、刷新去重由实现负责） |
| `HANDOFF(permission-mode)` | `rpc.go` `rpcStartSession` | 固定 `safe` → 放开 `interactive`/`judge`；前端必须先接住 `permission:request` 事件并回调 `ResolvePermission` |
| `HANDOFF(backend)` | `rpc.go` `rpcStartSession` | `SessionBackend` 空（workspace 本地 JSONL）→ Redis 热层 + DB 归档：实现 agentloop `sessions.Backend` 注入；**验收 = agentloop `sessions/backendtest` 契约测试全绿** |
| `HANDOFF(sandbox)` | `rpc.go` `rpcStartSession` | 多租户隔离：注入 `engine.Config.ToolEnv`（per-session HOME/代理/凭证）；更强的选择性沙盒实现 `engine.ToolRuntime`（agentloop 根包 `toolruntime.go`） |
| `HANDOFF(session-list)` | `rpc.go` `rpcListSessions` | 活动会话表 → 持久会话列表（`sessions.OpenBackend(workspace, kind).List`） |
| `HANDOFF(transport-ws)` | `events.go`（hub 注释） | SSE → WebSocket 升级：hub 原样可用，把 SSE handler 换成 WS 连接注册；trigger 命令的重试去重靠 WS 信封的 client request id（agentloop `docs/protocol.md` §7） |
| `HANDOFF(replay)` | `events.go` `handleEvents` | `?after=<seq>` 重放：hub 加每会话环形缓冲，订阅时补发 `seq > after` 的存量信封 |
| `HANDOFF(process-events)` | `events.go` `Emit` | `sessionId == ""` 的进程级事件目前丢弃；需要时开独立广播流 |

### 待填清单（骨架刻意不做的事）

- **WS 升级**：单连接多路复用 + client request id 去重（SSE 是协议允许的降级形态）。
- **多用户认证与 TokenSource 注入**：JWT/网关会话 → per-user `engine.Config.TokenSource`。
- **Redis 热层 + DB 归档 Backend**：实现 agentloop `sessions.Backend`，
  验收标准 = agentloop `sessions/backendtest` 契约测试。
- **选择性沙盒 ToolRuntime**：`engine.ToolRuntime` 网关运行时（本地工具 + 沙盒转发混合）。
- **部署**：容器化、水平扩展（会话粘滞或共享 Backend）、可观测性。
