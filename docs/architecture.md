# runcode 架构

本文档从当前代码整理，描述 runcode 的分层结构与各子系统边界。桌面版（XRUN）的细节见 [docs/desktop.md](./desktop.md)。

## 总览：一个引擎，三个前端

```text
┌ cmd/runcode chat ┐   ┌ cmd/runcode tui ┐   ┌ XRUN 桌面版（Wails）        ┐
│ shell 友好 CLI    │   │ Bubble Tea TUI  │   │ internal/desktop + 前端   │
└────────┬─────────┘   └───────┬─────────┘   └───────────┬──────────────┘
         └─────────────────────┴─────────────────────────┘
                    internal/engine —— 传输无关引擎门面
                    Config / Build / Session / discovery
                              │
                    internal/repl.Session —— ReAct 循环
        ┌──────────┬──────────┼──────────────┬──────────────┐
     tools/   permissions   prompt        pkg/llm      persistence
   内置+动态工具  四模式+harm  静/动态缓存边界  anthropic/openai  sessions/transcript/settings
```

三个前端都经 `engine.Build(cfg, engine.Options{...})` 拿到 `*engine.Session`，注入各自的流式回调、工具事件通道与审批器；引擎内部不含任何 UI 逻辑。

## 仓库布局

```text
cmd/runcode/            Cobra CLI：version、chat、tui、config、permissions、sessions、transcript
cmd/runcode-desktop/    嵌套 Go module：Wails 桌面外壳 + React 前端（见 docs/desktop.md）
internal/engine/        引擎门面：Config、Build、Session、skills/agents/memory 发现
internal/repl/          ReAct Session、executor、流式收集、harm judge、reasoning
internal/permissions/   动作/资源/风险模型、policy、审批、命令分类、harm gate、持久化规则
internal/prompt/        系统提示装配器与静/动态缓存边界（sections/）
internal/desktop/       桌面版传输无关核心（根模块内，不依赖 Wails）
internal/ui/            Bubble Tea TUI：视图、slash 命令注册表、会话选择器、审批桥
internal/mcp/           MCP 客户端：JSON-RPC、stdio + Streamable HTTP、tools/resources/prompts/roots/sampling
internal/subagent/      Task 工具与子代理 Launcher、内置代理、事件桥
internal/hooks/         8 个生命周期事件的用户钩子（argv 直执、JSON stdin）
internal/compaction/    token 预算触发的语义历史压缩
internal/cost/          内置模型定价表（最长前缀匹配）
internal/persistence/   sessions（jsonl/sqlite 全量历史）、transcript（脱敏审计）、settings（TOML）
internal/telemetry/     事件模型、JSONL/async/memory recorder、关联 ID
internal/toolpath/      workspace 路径解析、symlink 安全包含检查、读后写门
internal/projectctx/    RUNCODE.md / CLAUDE.md 项目上下文加载（向上查找，64 KiB 上限）
internal/diff/          Edit/Write 工具卡的有界统一 diff
internal/webclient/     SSRF 加固的 HTTP 客户端（WebFetch/WebSearch 共用）
pkg/tool/               公开工具 SDK：Tool 接口、Schema、Context、Result、Event、Registry
pkg/llm/                provider 中立 DTO、Stream、provider 注册表（init() 注册）
pkg/agent/              子代理定义：frontmatter 解析、目录发现、catalog
pkg/skill/              技能：渐进式披露 catalog + Skill 工具
pkg/memory/             跨会话记忆：两 scope 存储 + Remember 工具
pkg/command/            自定义 slash 命令（*.md 发现）
tools/                  内置工具实现与注册表
```

仍为 `.gitkeep` 空壳（无实现）：`internal/app`、`internal/coordinator`、`internal/session`、`internal/persistence/claudemd`、`internal/persistence/migrate`、`internal/persistence/sqlite`（实际 SQLite 代码在 `persistence/sessions/sqlite.go` 与 `persistence/transcript/sqlite.go`）、`pkg/plugin`。

## 引擎门面（internal/engine）

- **`Config`**（`config.go`）：一次会话的全部已解析配置——provider/model、凭证、CWD、权限模式、telemetry/transcript、会话 id 与 resume、历史/上下文预算、重试、单价、MCP servers、hooks、thinking、系统提示覆盖/追加，以及 `HarmJudgeModel`/`HarmJudgeVotes`。
- **`Options`**（`engine.go`）：纯数据/接口注入边界——`StreamDelta`、`StreamThinking`、`ToolEvents chan<- tool.Event`、`Permissions`（整个服务注入）或 `Approver`（仅审批器）、`Warn`、`TelemetryWriter`。零值 Options 得到丢弃警告的非交互 safe 会话。
- **`Build(cfg, opts)`**（`build.go`）：打开会话后端 → 解析会话 id（Resume/Continue/指定/新建）→ transcript recorder → provider（经 `llm.Build` 注册表）→ MCP manager（含 roots 与可选 sampler）→ 项目上下文 → 权限服务 → hook runner → bash manager → 工具集（内置 + MCP + Skill + Task + Remember）→ skills/agents/memory 发现 → `repl.NewSession`。失败时逐一关闭已建资源。
- **`Session` 方法**：`RunTurn`/`RunTurnWithImages`、`GenerateTitle`、`AssessHarm`、`ResetHistory`、`Compact`、`SetPermissionMode`、`SetModel`、`SetPlanMode`（同时切 repl 提示与权限层）、`SetReasoningScenario`、`SetThinkingEffort`、`ReloadSkills`/`ReloadAgents`（热重载）、`MCPStatus`、`ToolList`、`Status`、`Close`（触发 SessionEnd 钩子并关闭全部资源）。
- **发现助手**（`discovery.go`）：`LoadSkills`、`LoadAgents`（合并内置，优先级 user > project > builtin）、`MemoryStore`、`NewPermissionService`、`NewAllowStore`、`MCPServersFromConfig`。
- **harm judge 模型解析**（`build.go`）：显式 `HarmJudgeModel` 优先；anthropic（或未指定 provider）时默认独立的 `claude-haiku-4-5-20251001`，否则复用主模型——默认让安全门与主模型**去相关**。

## ReAct 核心（internal/repl）

`Session.runTurn`（`session.go`）单 turn 流程：

```text
UserPromptSubmit 钩子（可拦截/注入上下文）
  -> SessionStart 钩子（首 turn 一次）
  -> 快照历史 + 追加注入上下文与用户消息（含图片）
  -> 立即持久化开场消息（用户一发送会话即落盘）
  -> 可选 reasoning 分类/协议
  -> ReAct 循环（≤ MaxIterations，默认 8）：
       历史预算裁剪 -> 组装请求（系统提示 + tool specs）-> streamAssistant
       -> 无 tool_use：持久化 + commitTurn + Stop 钩子，返回最终文本
       -> 有 tool_use：executeToolUses -> 追加 tool 消息
          -> assistant+tool 成对持久化（日志绝不出现无结果的 tool_use）-> 继续循环
```

- **增量持久化**：每完成一步就 append 到 `SessionStore`，turn 中途崩溃也留下合法可恢复的日志；`commitTurn` 负责 transcript 记录与内存历史提交，不再整 turn 落盘。
- **流式**：文本 delta 走 `StreamDelta`、思考走 `StreamThinking`，工具调用参数在生成过程中实时流入 UI 卡片（Write/Edit 正文"打字机"式）。
- **工具并发**：连续的并发安全工具批量并行（errgroup），其余串行；用户"拒绝并停止"时为剩余 tool_use 填充跳过占位，保证消息结构完整。
- **运行时开关**：`SetModel`/`SetPlanMode`/`SetReasoningScenario`/`SetThinkingEffort`/`SetSkillsCatalog`/`SetAgentsCatalog` 均加锁，turn goroutine 与 UI 线程不竞态。
- **压缩**：`--max-context-tokens` 设置后，input tokens 达预算 80% 触发 `internal/compaction`——最旧 turn 经 LLM 增量总结为一条摘要消息，只动内存工作集，磁盘历史保持完整。
- **executor**（`executor.go`）：每次工具调用统一走 `AuthorizeTool` → 决策 telemetry → 拒绝时返回带**可操作指引**的 `is_error` 结果（如读后写恢复提示）→ PreToolUse 钩子 → 带 panic 恢复地运行工具 → PostToolUse 钩子 → 发出带脱敏输出/文件引用的完成/失败事件。judge/flight 放行会设置 `MetadataTrustedWrites` 让 Write/Edit 跳过自身读门；`AskUser` 置 `StopTurn`；工具 panic 转为 `is_error` 结果而非崩溃。

### Harm judge（模型判害门）

`Session.AssessHarm(ctx, facts, untrusted)`（`session.go`）是权限层 `HarmJudge` 的模型实现：

- 输入分**可信分类事实**（operation、命令分类/能力/风险原因、目标 scope——来自确定性分类器）与**不可信原文**（智能体自选的命令行/路径/host），后者用随机 per-call nonce 围栏包裹，注入载荷无法越狱；系统提示要求忽略原文中任何"已批准"声明，并把操纵企图本身视为有害信号。
- 输出要求紧凑 JSON `{"harmful":bool,"reason":"中文"}`；非纯 JSON 会带更严格指令重试一次，仍失败则**返回错误让调用方 fail-safe**（回落人工审批）。解析时忽略从不可信原文原样回显的 JSON 对象（防注入）。
- `HarmVotes > 1` 时按温度 1.0 采样 N 次取多数，平票偏有害，全部失败则 fail-safe。
- 判定模型经 `harmJudgeModel()` 取独立 `HarmModel`（未配置时用当前主模型）。

目前**只有桌面版接线了 harm judge**（`internal/desktop/harm.go`）；CLI/TUI 不注入 HarmJudge，也不暴露 judge/flight 模式。

## 权限系统（internal/permissions）

管线：`resolver.go` 把工具输入解析为 `Action{Operation, Risk, Resources, Metadata}`（Bash 先经 `command.go` 保守分类出 category/capabilities/risk reasons）→ `policy.go` `DefaultPolicy.Decide` 给出 allow/ask/deny → authorizer 把 ask 变成最终决定。

**四种模式**（`service.go`；CLI/TUI 只暴露前两种，桌面版暴露全部）：

| 模式 | 行为 |
|------|------|
| `safe` | 非交互；ask 一律转 deny。工作区内 Read/Glob/Grep 允许。 |
| `interactive` | 每个 ask 都询问用户；harm gate 被剥离（模型永不自动放行）。 |
| `judge` | 智能模式：(a) 工作区内常规文件变更直接自动放行（`ReasonJudgeAllowed`，敏感执行面文件除外）；(b) 其余 ask（命令/网络/external）过模型判害门——判"安全"则自动放行，判"有害"或判定失败则升级人工审批并附原因。 |
| `flight` | `BypassAuthorizer` 全放行。 |

judge 模式的三道防线：

- **确定性下限**（`judge_floor.go`）：模型判定永远不能自动放行——external MCP 调用（judge 看不到参数）、敏感文件变更（`.git`/`.github`/`.runcode` 等目录，`.bashrc`/`.env*`/`.npmrc`/`.netrc` 等文件）、privileged/outside-write/destructive-VCS 命令能力。
- **熔断器 + 审计**（`harm_breaker.go`）：每会话自动放行有预算（默认 50），超限转逐个确认；每次自动放行/熔断经 `HarmAuditFunc` 发出脱敏审计记录（桌面版转成 UI 事件）。
- 判定失败 fail-safe 回落人工审批（见上节）。

**规则持久化**：`<workspace>/.runcode/permissions.json`（`FileAllowStore`，0600，gitignore，原子写，deny 恒胜 allow），由 `runcode permissions list/remove/clear/deny/allow` 管理；进程内另有会话级 allow store。**plan mode**（`SetPlanMode`）在任何模式下都拒绝一切变更动作（带只读管道识别）。

## 工具系统

`pkg/tool` 定义公开 SDK（`Tool` 接口、`Schema`、`Context`+ReadSet、`Result`、流式 `Event`、重名即 panic 的 `Registry`）。`tools/registry.go` `Builtins()`/`BuiltinsWithShells()` 注册 **14 个内置工具**：

| 工具 | 行为 |
|------|------|
| `Read` | 读文件；文本带行号，图片（png/jpg/gif/webp）直接返回图像；更新 ReadSet。并发安全。 |
| `Write` | 创建新文件或覆盖已完整读取的文件。 |
| `Edit` | 对已读文件做精确字符串替换。 |
| `Delete` | 删除工作区文件/目录；默认进系统回收站（可恢复），`permanent=true` 不可逆。 |
| `Glob` | slash glob（含 `**`）文件发现。并发安全。 |
| `Grep` | 正则搜索；输出模式 content/files_with_matches/count、context 行、multiline。并发安全。 |
| `Bash` | 审批后执行非交互命令（Windows 用 cmd，其余 bash）；支持多行与 `run_in_background` 后台。 |
| `BashOutput` | 读取后台 shell 的新增输出并报告存活状态。 |
| `KillShell` | 终止后台 shell。 |
| `TodoWrite` | 记录当前任务清单（整单替换）；免审批。 |
| `WebFetch` | 抓取 http(s) URL，HTML 转纯文本；SSRF 加固、浏览器 UA；network 操作需按 host 审批。 |
| `WebSearch` | 经 DuckDuckGo 无 JS 页面搜索，返回标题/URL/摘要。 |
| `Analyze` | 结构化分析步骤记录（仅思考协议激活的 turn 内出现）。 |
| `AskUser` | 向用户提问并停止 turn 等待回复。 |

`engine.Build` 时动态追加：**MCP 工具**（`mcp__<server>__<tool>` + 按能力出现的 resources/prompts 工具）、**`Skill`**、**`Task`**、**`Remember`**。Bash/BashOutput/KillShell 共享一个 `bash.Manager`，后台 shell 可读可杀、会话关闭时统一清理。

## 提示词系统（internal/prompt）

`assembler.go` 把系统提示构建为一张 section 表，以 `DynamicBoundary` 分隔缓存边界：静态段跨 turn 不变（provider 支持时携带 ephemeral cache 提示），动态段每 turn 重算。

- **静态段**（`sections/static.go`）：Intro + System（可被 `SystemPromptOverride` 整体替换）→ 子代理 persona（仅子会话）→ UsingTools（工具名+描述）→ Skills catalog → Agents catalog → Actions → ToneAndStyle → `SystemPromptAppend`。
- **动态段**（`sections/dynamic.go`）：reasoning guidance（思考场景）→ PlanMode（开启时）→ 环境信息（CWD/日期/shell 指引，Windows 有 cmd/PowerShell 与正斜杠路径提示）→ 权限模式指引 → Memory → 项目上下文。

reasoning 体系也在此：`ReasoningClassifier`（把任务归入 troubleshooting/proposal/architecture/project_management/incident_response/general 六场景）与 `ReasoningGuidance`（每场景一个命名思考模型 + 10 步清单），由 `repl.ReasoningOptions`（关闭/自动分类/手动指定）与 turn 内 Analyze 工具门驱动。

## Provider 层（pkg/llm）

中立 DTO（`Request`/`Message`/`ContentBlock`/`Stream`/`ToolSpec`/`ThinkingConfig` off·low·medium·high）+ `registry.go` 工厂注册表（provider 经 `init()` 副作用导入注册，`engine/build.go` 触发）。

- **anthropic**：官方 Go SDK，流式；原生 cache control 与 thinking budget；图片输入；SDK 内置重试。
- **openai**：直连 Chat Completions HTTP/SSE（无 vendor SDK），兼容 vLLM/Ollama/llama.cpp/网关；`tool_use↔tool_calls`、图片→`image_url`、流式 delta 重组；reasoning 模型的 `reasoning_effort`；transport 层连接期重试；调试用 `RUNCODE_SSE_DUMP_DIR` 原始 SSE 抓取与 `RUNCODE_OPENAI_DISABLE_USAGE_STREAM` 逃生阀。

重试策略统一：仅连接建立阶段，capped 指数退避，尊重 `Retry-After`，`--max-retries` 配置。

## 持久化

| 存储 | 位置 | 内容 |
|------|------|------|
| 会话历史 | `.runcode/sessions/<id>.jsonl` 或 `.runcode/sessions.db` | 无损全量 `llm.Message`，turn 内逐步 append；供 `--resume`/`--continue`、`runcode sessions`、TUI picker、桌面恢复；标题 sidecar（`titles.go`）。后端经 `sessions.Backend` 接口选择（`--session-backend jsonl|sqlite`，SQLite 为纯 Go `modernc.org/sqlite`）。 |
| transcript 审计 | `.runcode/transcripts/<id>.jsonl` 或 `.runcode/transcripts.db` | 白名单脱敏 turn 摘要，默认关闭（`--transcript jsonl|sqlite`）；SQLite 带 FTS5 trigram 全文索引，`runcode transcript list/search` 检索。与会话历史分离，不用于 resume。 |
| 设置 | 项目 `runcode.toml`（向上发现）+ 用户 `<UserConfigDir>/runcode/config.toml` | 优先级 flag > env > 项目 > 用户 > 默认；**凭证、MCP servers、hooks 只认用户级文件**（项目文件出现即剥离）。 |
| 权限规则 | `.runcode/permissions.json` | allow/deny 列表（见权限系统）。 |
| 记忆 | 用户 `<UserConfigDir>/runcode/memory.md` + 项目 `.runcode/memory.md` | 单行 bullet 事实，`Remember` 工具去重追加。 |

## 扩展系统

- **MCP**（`internal/mcp`）：从零实现的 JSON-RPC 2.0 客户端，stdio 与 Streamable HTTP 两种传输；tools/resources/prompts/roots/sampling 全原语。容错启动（单 server 失败只告警）；sampling 默认关、显式开启且 safe 模式恒拒。server 仅用户级配置，`${VAR}` 展开。
- **Skills**（`pkg/skill`）：`SKILL.md` 目录约定发现（用户级 + 项目级，user 遮蔽同名 project）；只有 catalog 进提示，正文经 `Skill` 工具按需披露（免审批）。
- **Sub-agents**（`pkg/agent` + `internal/subagent`）：`Task` 工具委托受限子 `repl.Session`（自有 persona/可选 model/受限工具集/共享父权限服务），恰好一层不嵌套；一 turn 多个 `Task` 并发 fan-out（默认上限 8，interactive 下串行）。内置代理：`general-purpose`、`code-reviewer`、`code-explorer`、`planner`（只读）、`debugger`；优先级 user > project > builtin。
- **Hooks**（`internal/hooks`）：8 个事件——`PreToolUse`、`PostToolUse`、`UserPromptSubmit`、`Stop`、`SubagentStop`、`SessionStart`、`SessionEnd`、`PreCompact`。argv 直接执行（无 shell），JSON payload 经 stdin，非零退出=拦截/反馈，运行失败 fail-open 告警。仅用户级配置。
- **自定义 slash 命令**（`pkg/command`）：用户/项目 `commands/` 目录下的 `*.md`，合入 TUI 命令菜单。
- **记忆**（`pkg/memory`）：`Remember` 工具写固定路径两 scope 文件，`manage` 分类免审批；子代理只读。
- **成本**（`internal/cost`）：内置定价表按 model 名最长前缀匹配，显式单价恒胜、未知 model 不计价；供 `/cost` 与 `runcode config`。

## CLI 参考（cmd/runcode）

七个子命令：`version`、`chat`（`--loop` 多轮）、`tui`（`--pick` 会话选择器）、`config`（打印生效配置与来源，凭证脱敏）、`permissions`、`sessions`（`list`/`show`）、`transcript`（`list`/`search`）。

主要 flag 与环境变量（`chat`/`tui` 共用，config 解析在 `resolveChatConfig`）：

| flag | env |
|------|-----|
| `--provider` | `RUNCODE_PROVIDER` |
| `--model` / `--max-tokens` / `--base-url` | `ANTHROPIC_MODEL` / `ANTHROPIC_MAX_TOKENS` / `ANTHROPIC_BASE_URL` |
| `--api-key` / `--auth-token` | `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` |
| `--cwd` | `RUNCODE_CWD` |
| `--permission-mode safe\|interactive` | `RUNCODE_PERMISSION_MODE` |
| `--thinking off\|low\|medium\|high` | `RUNCODE_THINKING` |
| `--system-prompt` / `--append-system-prompt` | `RUNCODE_SYSTEM_PROMPT` / `RUNCODE_APPEND_SYSTEM_PROMPT` |
| `--telemetry off\|jsonl` | `RUNCODE_TELEMETRY` |
| `--transcript off\|jsonl\|sqlite` / `--session-id` | `RUNCODE_TRANSCRIPT` / `RUNCODE_SESSION_ID` |
| `--resume` / `--continue` / `--no-session` / `--session-backend` | `RUNCODE_SESSION_BACKEND` 等 |
| `--max-history-messages` / `--max-context-tokens` | `RUNCODE_MAX_HISTORY_MESSAGES` / `ANTHROPIC_MAX_CONTEXT_TOKENS` |
| `--max-retries` / `--input-price` / `--output-price` | `RUNCODE_MAX_RETRIES` / `RUNCODE_INPUT_PRICE` / `RUNCODE_OUTPUT_PRICE` |
| `--allow-mcp-sampling` | `RUNCODE_ALLOW_MCP_SAMPLING` |

注意：CLI/TUI 的权限模式只接受 `safe`/`interactive`；`judge`/`flight` 与 `HarmJudgeModel`/`HarmJudgeVotes` 目前是桌面版（及 `RUNCODE_HARM_JUDGE_MODEL`/`RUNCODE_HARM_JUDGE_VOTES` 环境变量）专属。

TUI 内置 slash 命令（`internal/ui/commands.go` 注册表驱动）：`/help`、`/clear`、`/compact`、`/status`、`/mode safe|interactive`、`/model <name>`、`/cost`、`/exit`（`/quit`），另合并自定义 `.md` 命令。

## 观测（internal/telemetry）

统一事件模型（turn / LLM request / tool execute 生命周期 + `permission.decision` + 压缩/持久化错误），trace/turn/request/tool_use 关联 ID。默认 noop；`--telemetry jsonl` 经有界异步 recorder 写 stderr（队列满丢弃，不阻塞主链路）。permission 与 tool error telemetry 只记脱敏元数据——不记原始路径、命令、工具输入输出、文件内容、凭证或 URL。

## 验证

```bash
go build ./...                          # 核心全量编译（不含桌面嵌套模块）
go test -race ./...                     # CI 同款，三平台
golangci-lint run                       # gosec/errcheck/gocritic
go -C cmd/runcode-desktop build ./...   # 桌面 Go 侧快速检查
cd cmd/runcode-desktop && wails build   # 桌面正式打包 -> build/bin/XRUN.exe
```
