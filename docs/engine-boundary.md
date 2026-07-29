# 引擎与外壳的边界调整方案（以 MCP 为切口）

状态：已确认（2026-07-29）｜批次 0 已收尾，下一步批次 1 | 分支：`refactor/engine-shell-boundary` | 引擎：`../agentloop`

## 0. 判据

划边界的依据不是"这个需求来自谁"，而是**谁定义语义、谁提供机制**。

- **机制**归引擎（或引擎仓库里的共享库）：协议实现、传输、工具装配、权限判定、回合调度。三个客户端（CLI、桌面、服务端骨架）共用同一份。
- **语义**归客户端：连哪些服务器、带谁的凭据、信不信它、装哪个、在哪执行。

按这条判据，"MCP 是客户端业务，所以实现该搬到客户端"只对了一半：MCP 协议栈是机制，必须留在共享层；而"通行证""可信""沙盒执行"是语义，现在有一部分漏进了引擎。

## 1. 现状诊断

### 1.1 发布链路当前是断的

`GOWORK=off go build ./...`（CI 与发布用的模式）编译失败，本仓库已经在用引擎里尚未发布的 API：

```
permissions.DefaultPolicy.TrustedMCPServers   / engine.TrustedMCPServers
settings.MCPServerConfig.Passport
mcp.ServerConfig.HeaderSource / .Trusted
host.Manager.Inject
```

引擎侧 27 个文件的改动全部躺在 `D:\agentloop` 工作区，未提交、未打 tag，而本仓 require 的是 `v0.6.0`。本地能跑只是 `go.work` 兜着。**在此状态下做任何边界改动都无法验证。**

### 1.2 引擎侧积压改动按边界分类

| 类别 | 内容 | 判断 |
| --- | --- | --- |
| 纯引擎能力 | openai Responses 流式修复、`tools/read`、`host.Manager.Inject`（中途插话） | 位置正确 |
| 端口式设计 | `mcp.ServerConfig.HeaderSource`（每请求取凭据的注入点，引擎不知道"通行证"是什么） | 形式正确，保留 |
| **语义泄漏** | `settings.MCPServerConfig.Passport`、`mcp.ServerConfig.Trusted`、`engine.TrustedMCPServers`、`DefaultPolicy.TrustedMCPServers` 的取值来源 | 本方案要纠正的 |

### 1.3 泄漏是怎么发生的

**`Passport` 字段**：注释自证——*"Desktop-only... the engine parses it but does not act on it"*。引擎解析一个自己从不使用的桌面字段，唯一原因是 config.toml 的解析恰好在引擎的 `settings` 包里。**语义是顺着配置解析这条路漏进去的，不是顺着功能。**

**`Trusted` 字段**：因为它被塞进了 `mcp.ServerConfig`，桌面自建 permission service 时得重算同一个集合，于是引擎不得不导出 `TrustedMCPServers()`（`build.go:391`），注释写着"deriving it twice from different rules is how a server ends up trusted in one build and prompting in another"。**这个函数存在本身，就是字段放错位置打的补丁。**

而桌面其实已经有权威集合了——`passportMCPNames()`（`internal/desktop/mcppassport.go:24`）。信任集合从"桌面已知"绕经"写进引擎配置结构 → 引擎导出函数再算一遍 → 传回桌面"，绕了一整圈。

### 1.4 机制侧是干净的（所以语义可以安全搬走）

- MCP 工具和 host 注入的 `ExtraTools` 走同一条汇入路径、同一个 `tool.Tool` 接口（`build.go:156` 与 `:235`）。
- 权限层识别 MCP 只靠工具名前缀 `mcp__server__tool`（`permissions/resolver.go:378`），不依赖 mcp 包的对象。
- 传输层下面已有接缝 `messageStream`（`mcp/transport.go`），JSON-RPC 层不知道自己跑在什么传输上。

## 2. 目标与非目标

**目标**

1. 引擎不再认识任何下游产品概念（通行证、租户、平台自建）。
2. 桌面加功能不再需要给引擎发版——除非用到的是引擎真的没有的机制。
3. 为服务端"stdio MCP 需在沙盒执行"留好注入点，且不必为此重装配 MCP 协议栈。
4. 恢复 `GOWORK=off` 可构建可测。

**非目标**

- 不把 mcp 包搬到桌面外壳（见 §6）。
- 不在本轮实现沙盒 launcher（只留端口）。
- 不动 http transport 的装配方式。

## 3. 第 0 步：止血（引擎 v0.7.0）

先把现有积压原样落地，不夹带任何设计改动，得到一个"已知好点"以便回退。

1. 引擎侧 27 个文件按主题分批提交（provider 修复 / read 工具 / Inject / MCP HeaderSource+Trusted / permissions / settings）。
2. 打 `v0.7.0`，推内网 GitLab。
3. 本仓 `go.mod`（根 + `cmd/runcode-desktop`）升 require 到 `v0.7.0`。
4. 验证：`GOWORK=off GOPRIVATE=gitlab.ouc-online.com.cn go build ./...` 通过；`go test -race ./internal/... ./cmd/runcode/...` 通过。

> 顺带：`go.mod` 里 `github.com/xuri/excelize/v2` 应从 indirect 提为直接依赖（officetool 直接引用）。

## 4. 方案 A：MCP 语义上移（引擎 v0.8.0，破坏性变更）

### A1 `settings` 给通用扩展点，不再认识 `passport`

引擎不该为下游字段改结构体。改为：解析时保留未识别的键，原样透传给 host。

```go
// settings.MCPServerConfig
type MCPServerConfig struct {
    Transport string            `toml:"transport"`
    Command   string            `toml:"command"`
    // ... 已知字段不变 ...
    Enabled   *bool             `toml:"enabled"`

    // Extra 收集本结构未识别的键，原样交给 host。引擎不解释其含义，
    // 也不因为下游多一个开关而改动本结构。
    Extra map[string]any `toml:"-"`
}
```

实现方式（二选一，推荐前者）：

- **给 `MCPServerConfig` 实现 `toml.Unmarshaler`**（pelletier/go-toml v2 支持）：先解到 `map[string]any`，取走已知键，其余进 `Extra`。内聚在类型自身，`settings.Load` 不用改。
- 或在 `settings.Load` 里二次解析整份文档，按 server 名把未知键回填。改动面更大，不推荐。

**删除 `MCPServerConfig.Passport`。**

桌面侧适配（`internal/desktop/mcppassport.go:24`）：

```go
// passportMCPNames：改读 Extra["passport"]，语义解释权回到桌面
if v, ok := s.Extra["passport"].(bool); ok && v { out[name] = true }
```

**兼容性**：`config.toml` 里已有的 `passport = true` 无需改动，仍然生效——只是解释它的人从引擎换成了桌面。

### A2 `Trusted` 归位：从配置结构移到权限装配

- **删除** `mcp.ServerConfig.Trusted`。
- **删除** `engine.TrustedMCPServers()`（`build.go:391`）及 `newPermissionService` 的 trusted 参数，`NewPermissionService` 恢复原签名。
- **保留** `permissions.DefaultPolicy.TrustedMCPServers`——它是策略的通用能力（host 声明一组免逐次审批的服务器），不含任何产品概念。

桌面侧（`internal/desktop/app.go:247`）由绕行改为直给：

```go
// 改前：Policy: permissions.DefaultPolicy{TrustedMCPServers: engine.TrustedMCPServers(cfg.MCPServers)}
// 改后：
Policy: permissions.DefaultPolicy{TrustedMCPServers: passportMCPNames()},
```

`applyMCPPassport`（`mcppassport.go:48`）随之只负责挂 `HeaderSource`，不再设 `Trusted`。

**CLI 与服务端不受影响**：它们没有平台自建服务器，可信集合为空，行为与今天一致（外部 MCP 调用照常逐次审批）。

### A3 改完之后引擎还认识 MCP 的什么

只剩机制：这是一种工具来源、它的调用是 `OperationExternal`、它的传输是 stdio 或 http。产品概念一个不剩。

## 5. 方案 A+：stdio 执行位置端口化（纯加法）

### 5.1 问题

stdio MCP 服务器在桌面是本地起进程即可，在服务端必须进沙盒。差异**只在"进程在哪儿起、管道怎么接"**，协议层完全一样。

### 5.2 引擎已有同构先例

内置工具的"在哪执行"已经端口化了——`ToolRuntime`（`toolruntime.go:35-44`），注释里甚至点名 MCP 是先例：

> A server host may inject a gateway runtime whose toolsets mix local tools with sandbox-forwarded ones — `tool.Tool` is the execution-location-independent contract ... (the MCP manager is the existing precedent).

MCP 照抄同一范式即可，**不要另起一套**。

### 5.3 接缝选在传输层（最低那层）

`newTransport()`（`mcp/manager.go:223`）里的 `switch cfg.Transport` 是包内硬编码，只此一处调用。把 stdio 那一支变成可注入：

```go
// mcp.Options 新增字段（nil = 现在的本地实现，行为不变）
Launcher Launcher

// Launcher 启动一个 stdio MCP 服务器实例。进程在哪儿起、工作区怎么映射进去，
// 全归 host；引擎只要一个能读写它 stdin/stdout 的字节流。
type Launcher interface {
    Launch(ctx context.Context, cfg StdioConfig) (io.ReadWriteCloser, error)
}

// 可选：实现了此接口的返回值，其诊断信息会用于握手失败时的报错
// （对应现有 stdioStream.Diagnostics：stderr 尾部 + 进程退出状态）。
type Diagnoser interface{ Diagnostics() string }
```

帧化（`newFrameStream`）留在引擎，host 不必懂 JSON-RPC——契约就是一对管道。JSON-RPC、握手、工具发现、resources/prompts/roots、sampling、重连全部原样复用，服务端一行都不用重写。

### 5.4 端口契约（不写清楚会出事）

1. **只覆盖 stdio。** http 在服务端与桌面是一样的出站 HTTP，凭据与代理已由 `HeaderSource` 覆盖。别把 http 拖进来。
2. **凭据清洗责任随端口转移。** 现在本地实现会 `secenv.Sanitize(os.Environ())` 洗掉继承的 API key，防止 MCP 服务器读到 agent 自己的凭据（`mcp/stdio.go:79`）。自定义 launcher 若忘了做就是凭据泄漏——**契约必须写明，并配一条单测钉住默认实现的清洗行为**。
3. **可能被重复调用。** 重连走的是同一条 `dialClient` 路径（`manager.go:202`），launcher 要能被多次调用。
4. **caller-owned。** 与 `ToolRuntime` 一致：注入的 launcher 由 host 持有和关闭，引擎不碰；一个沙盒客户端会服务很多会话。
5. **工作区映射归 host。** `StdioConfig.Dir` 是逻辑工作区路径，沙盒里怎么挂载、映射成什么路径是 launcher 自己的事。

### 5.5 取舍：现在做端口，不做实现

`Launcher` 是 `Options` 加字段，**纯加法、非破坏性、随时可加**。因此：

- **建议**随 v0.8.0 一并放出接口 + 默认实现 + 契约单测（成本极小，且省掉服务端做沙盒时再给引擎发一次版）。
- **明确不做**沙盒 launcher 的实现——服务端沙盒尚无具体形态，现在写就是过度工程。

## 6. 明确不做：把 mcp 搬到客户端（原 B 方案）

两条硬理由：

1. **mcp 包是三端共用的协议实现。** 搬进桌面外壳，CLI 与服务端要么各抄一份，要么反过来依赖桌面外壳，依赖方向当场坏掉。
2. **sampling 是真实的反向依赖。** MCP 服务器可反过来请求宿主做 LLM 采样，引擎为此要给它 provider + model + 审批门（`build.go:106-115`）。mcp 若搬到外壳，外壳得先拿到引擎的 provider 和 approver 再回注，依赖绕一圈回来。

做完 A + A+ 后，引擎认识的只剩"MCP 是一种工具来源"这个机制事实，B 的剩余收益接近于零。**只有当某一端需要完全不同的连接策略时**（如服务端连接池化、多租户共享连接）才重新评估，届时 A/A+ 的成果全部保留。

## 7. 同源问题：`CommandKinds`（建议同批处理）

`protocol.CommandKinds` 是同一类泄漏的另一处：命令清单是客户端概念，却住在引擎，导致**桌面加一个 Wails 命令就要改引擎、打 tag、升 require**——本轮 4 个新命令（`McpMarket`/`ReloadMCPServers`/`InjectMessage`/`InjectMessageWithImages`）正是这么加的。引擎自己的 `protocol/doc.go` 已承认这是"the one deliberate exception"。

处理方式：命令名与幂等类别的单一事实源迁到本仓 `internal/protocol`，引擎只保留 `CommandKind` 类型定义。`cmd/runcode-server` 与 `tools/protogen` 改从本仓取（`cmd/runcode-server` 在 `github.com/wt68/runcode/...` 路径前缀下，可以 import 根模块的 `internal`）。

**已确认并入 v0.8.0**：破坏性变更一次做完，此后加 Wails 命令不再需要碰引擎。

## 8. 验证与回归护栏

每批改动后：

- `GOWORK=off GOPRIVATE=gitlab.ouc-online.com.cn go build ./...`（发布链路，**必须过**）
- `go build ./... ; go -C cmd/runcode-desktop build ./...`（go.work 联动）
- `go test -race ./internal/... ./cmd/runcode/...`；`go -C cmd/runcode-server test -race ./...`
- `go run ./tools/protogen --check`（协议 TS 防漂移）
- `golangci-lint run ./...`（新增告警按回归处理）
- 前端（`frontend/`）：`npm run typecheck`、`npm run lint`、`npx vitest run`

新增单测（防止边界再次被越过）：

| 测试 | 钉住的事实 |
| --- | --- |
| `settings`：未知键进 `Extra` 且已知键不受影响 | 扩展点可用，引擎不必为下游改结构 |
| `mcp`：默认 launcher 清洗继承的凭据环境变量 | §5.4 契约 2 |
| `mcp`：注入 launcher 时不再走本地 `exec` | 端口真的生效 |
| `desktop`：`passport` 从 `Extra` 读出，缺失/非 bool 时不启用 | 语义解释权在桌面 |
| `desktop`：可信集合与 passport 集合同源 | 消除"两处各算一遍" |

## 9. 执行顺序

| 批次 | 内容 | 产物 |
| --- | --- | --- |
| 0 | 引擎积压落地（§3） | ✅ 已完成：引擎 `v0.7.0` 已推送；本仓 require 已升；`GOWORK=off` 恢复可构建可测 |
| 1 | A1 + A2（§4） | 引擎 `v0.8.0`（破坏性）；本仓适配 |
| 2 | A+ 端口 + 契约单测（§5） | 与批次 1 同一 tag |
| 3 | `CommandKinds` 回迁（§7） | 并入 `v0.8.0` |

批次 1、2、3 都改引擎 API，合并成一个 tag 发布——一次破坏性变更好过三次。

### 批次 0 已完成的内容

引擎侧 27 个文件按 6 个主题提交（`fc2f528`..`71ce978`），打 `v0.7.0`：

1. `fix(llm/openai)` Base URL 指向站点返回 HTML 200 → 触发 /v1 探测并报出真因
2. `fix(llm/openai)` 流被优雅掐断 → 报可重试传输错误，不再提交半截回答
3. `feat(tools/read)` 二进制文件不倾倒原始字节，改给可执行指引；`ReadOffice` 归为 manage
4. `feat(engine)` 回合中途插话 `Inject`（repl / engine / host 三层同一哨兵）
5. `feat(mcp)` http 传输 `HeaderSource` 每请求取凭据；config 加 `passport` 开关
6. `feat(permissions)` `DefaultPolicy.TrustedMCPServers` 免逐次审批
7. `protocol` 登记桌面新增的 4 个命令（message 里记了这正是 §7 要消除的税）

验证：`go build ./...` 通过；`golangci-lint run ./...` 0 issues；`go test -race ./...` 除
`tools/bash` 一条环境敏感用例外全过——该用例在 `v0.6.0` 上以同样方式失败，与本批无关
（`ShellName()` 直接返回 `$SHELL`，Git Bash 下是完整路径，而测试只设了 `RUNCODE_SHELL`）。

收尾（本仓 `83ba0ef`）：`v0.7.0` 已推送内网 GitLab，两个模块 require 升到 v0.7.0，
`go.sum` 里 v0.3.0~v0.6.0 的死校验和一并 tidy 掉。**`GOWORK=off` 下根模块与桌面模块
均编译通过、`go test -race` 通过**，`go.work` 联动模式不受影响，`protogen --check` 无漂移。
批次 1 起改引擎，从此有可验证的基线。
