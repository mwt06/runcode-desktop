# runcode 当前实现状态与缺口

日期：2026-06-02

本文档记录当前代码已经真实实现了什么、具体效果是什么、哪些地方只是为了保持最小化而做了半闭环，以及后续还缺少哪些能力。它是一个当前状态快照，不替代 `docs/architecture.md` 的架构说明，也不替代 `docs/session-handoff.md` 的历史日志。

## 总体状态

`runcode` 目前已经不是纯脚手架。它已经有一个最小可运行的 AI 编程伴侣核心闭环：

```text
cmd/runcode chat
  -> Anthropic provider
  -> internal/repl.Session
  -> prompt.BuildSystemPrompt + tools.Builtins tool specs
  -> model stream
  -> tool_use
  -> internal/repl.Executor
  -> internal/permissions.Service
  -> Tool.Run
  -> tool_result
  -> model final text
```

当前可用能力包括：

- 最小 CLI：`version`、`chat`、`chat --loop`、`tui`。
- Provider-neutral LLM 抽象。
- Anthropic streaming provider。
- 有限 ReAct session loop。
- 进程内多轮 history。
- 内置工具：`Read`、`Write`、`Edit`、`Glob`、`Grep`、`Bash`。
- 统一权限系统：safe / interactive、approval、permission telemetry。
- prompt assembler 与静态/动态 cache boundary。
- telemetry event model、JSONL stderr 输出、async recorder。
- opt-in JSONL transcript store。
- 最小 Bubble Tea TUI MVP：Claude Code 风格底部状态区、累计上下文 token 与思考模式指示、可滚动对话 viewport、上下分隔线包裹的单行输入、assistant 流式 Markdown 渲染、带安全文件摘要的树状工具进度卡片，以及 `/help` / `/clear` / `/status` / `/exit`。

但整体仍是 `v0.1-alpha` 最小实现。一些目录仍是空壳，部分能力只做到安全可验证的第一版，尚无 diff viewer、hooks、sub-agents 等。（permission modal、rich tool output、context compaction、OpenAI provider、MCP tools、skills 已实现。）

## 当前已实现模块

### CLI

关键文件：

- `cmd/runcode/main.go`
- `cmd/runcode/chat.go`
- `cmd/runcode/tui.go`
- `cmd/runcode/line_input.go`
- `cmd/runcode/approval.go`

已实现效果：

- `runcode version` 输出版本、commit、build time、Go 平台信息。
- `runcode chat [prompt]` 可从 args 或 stdin 读取 prompt。
- `runcode chat --loop` 可在同一进程中逐行对话并复用一个 session，`/clear` 可清空该 session 的内存 history。
- `runcode tui` 启动最小 Bubble Tea TUI，包含 Claude Code 风格底部状态区、累计上下文 token 与思考模式指示、可滚动对话 viewport、上下分隔线包裹的单行输入、assistant 流式 Markdown 渲染、带安全文件摘要的树状工具进度卡片，以及 `/help` / `/clear` / `/status` / `/exit`；`--permission-mode interactive` 下提供权限审批弹窗（允许一次 / 本会话允许 / 拒绝）和会话级权限记忆；工具卡片展示脱敏输出摘要（Bash stdout/stderr、Grep 匹配行、Read 预览）与 Edit/Write 完整行级 diff，可 `ctrl+o` 展开。
- `--provider` 目前只支持 `anthropic`。
- 支持 model、max tokens、base URL、API key、auth token、cwd、telemetry、permission mode 配置。
- 支持环境变量：`RUNCODE_PROVIDER`、`ANTHROPIC_MODEL`、`ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_BASE_URL`、`ANTHROPIC_MAX_TOKENS`、`RUNCODE_CWD`、`RUNCODE_TELEMETRY`、`RUNCODE_PERMISSION_MODE`、`RUNCODE_TRANSCRIPT`、`RUNCODE_SESSION_ID`、`RUNCODE_MAX_HISTORY_MESSAGES`。
- `--max-history-messages` / `RUNCODE_MAX_HISTORY_MESSAGES` 限制每轮发送给 provider 的内存 history 消息数；`0`（默认）表示不裁剪。
- `--telemetry jsonl` 会把 telemetry event 以 JSONL 写到 stderr。
- `--transcript jsonl` 会把成功 turn 的白名单摘要写到 `<workspace>/.runcode/transcripts/<session-id>.jsonl`。
- `--permission-mode interactive` / `confirm` 会在 stderr 提示一次性审批。
- approval prompt 只显示脱敏摘要，不显示 raw path、raw command、file content、credential 或 URL。

最小化缺口：

- `chat --loop` 只是逐行循环；完整交互体验由 `runcode tui` MVP 起步但仍不完整。
- TUI 已有可随内容增高的多行输入（Enter 发送、`alt+enter`/`ctrl+j` 换行）与已提交输入的 readline 式历史翻阅（↑/↓ 在首/末行翻历史，多行草稿内则移动光标；保留浏览前的草稿）；仍缺命令补全、可配置快捷键、按可视行(软换行)精确计高。
- slash 命令已有可扩展基座（含 `/mode`、`/model`、`/compact`、`/cost`）；后续可加更多命令。
- 已有 TUI permission modal（allow once / allow session / deny）、会话级权限记忆，以及 rich tool output（输出摘要 + Edit/Write 行级 diff）；仍缺权限策略持久化、syntax highlighting 与 side-by-side diff。
- 已有 TOML 配置文件系统(项目级 `runcode.toml` + 用户级 `config.toml`,优先级 flag > env > 项目 > 用户 > 默认,凭证仅用户级)与 `runcode config` 查看命令;尚无配置写入命令、热重载或迁移。
- 已有完整会话历史持久化(`.runcode/sessions/<id>.jsonl`,默认开启)与 `--resume`/`--continue` 跨进程恢复;尚无 transcript 浏览/检索界面。
- 非 loop 且无 args 时会读取 stdin 到 EOF，不是交互式输入体验。
- 只支持 Anthropic provider。

### LLM 抽象与 Anthropic provider

关键文件：

- `pkg/llm/provider.go`
- `pkg/llm/message.go`
- `pkg/llm/stream.go`
- `pkg/llm/providers/anthropic/provider.go`
- `pkg/llm/providers/anthropic/sdk.go`
- `pkg/llm/providers/anthropic/convert.go`
- `pkg/llm/providers/anthropic/stream.go`

已实现效果：

- 定义 provider-neutral `Provider`、`Request`、`Message`、`ContentBlock`、`Stream`、`StreamEvent`、`ToolSpec`。
- 支持 text、tool_use、tool_result、thinking、image 这些中立 content block 类型。
- 支持 cache control 标记。
- Anthropic provider 使用官方 Go SDK streaming API。
- Anthropic request conversion 支持 model、max tokens、temperature、system、messages、tools。
- Anthropic stream conversion 支持 text delta、tool input JSON delta、thinking delta、signature delta、stop reason、usage。
- 支持 API key、auth token、base URL。

最小化缺口：

- OpenAI 兼容 provider 已实现（`pkg/llm/providers/openai`）：直连 Chat Completions HTTP/SSE，支持 system 合并、`tool_use`↔`tool_calls`、`tool_result`→`tool` 消息、image→`image_url`、流式 delta 重组、usage 与 stop reason、可选 bearer；尚未支持 retry/backoff、parallel tool 控制、response format、reasoning channel。
- Anthropic provider 是最小 skeleton：没有 retry/backoff、rate limit 分类、timeout 配置、HTTP client 注入。
- 中立层定义了 image，但 Anthropic converter 当前不支持 image block。
- 不支持 stop sequences、top_p/top_k、tool choice、parallel tool use 控制、response format、thinking budget。
- `Request.Metadata` 没有透传到 Anthropic request。
- 没有 non-streaming provider API。
- capabilities 目前只提供粗粒度字段，session 还没有真正用它做请求适配。

### ReAct session 与 executor

关键文件：

- `internal/repl/session.go`
- `internal/repl/executor.go`
- `internal/repl/toolspec.go`
- `internal/repl/tool_result.go`
- `internal/repl/reasoning.go`
- `internal/repl/telemetry.go`

已实现效果：

- `Session.RunTurn` 构造 system prompt、messages、tool specs 并调用 provider streaming。
- 支持有限 ReAct loop：assistant tool_use -> executor -> tool_result -> provider next request。
- 支持 max iterations，默认 8。
- 支持同一 session 内进程级 history。
- 支持可选 `MaxHistoryMessages`：开启后裁剪发送给 provider 与提交回内存的旧历史，保留最新若干完整 turn，永不裁掉当前 turn，也不拆散 `tool_use`/`tool_result` 配对；`0`（默认）不裁剪；裁剪不触碰 transcript 文件。
- 成功 turn 会提交 history；开启 transcript 时还会写一条 JSONL turn summary；失败 turn 不提交、不写 transcript，避免污染后续上下文。
- 支持 `History()` clone 和 `ResetHistory()`。
- 支持 optional reasoning classification，将用户任务归类后注入动态 reasoning guidance。
- `Executor` 在任何工具运行前统一调用 `permissions.Service.AuthorizeTool`。
- permission denied 被转换成带脱敏 reason/final effect 的 `is_error=true` tool result 回传模型。
- unknown tool 和普通工具 runtime error 会转换成 `is_error=true` tool result，让模型有机会自我修正。
- 工具执行 telemetry 和 permission telemetry 已接入。
- 连续的 concurrency-safe tool_use 会批量并发执行；当前 `Glob` 和 `Grep` 标记为并发安全，interactive approval 可用时退回串行。

最小化缺口：

- transcript 只保存白名单摘要，不保存可恢复完整 history。
- 已有按 message-count 的 history budget(默认关闭)、token 预算触发的语义 context compaction(LLM 总结最旧 turn)、以及完整历史持久化 + resume;token 量用 provider 回传的 input tokens 近似,非本地精确计数。
- 已支持 assistant text delta streaming 和 executor-level tool lifecycle events，TUI 可展示带安全文件摘要的树状工具进度卡片；Read/Glob/Grep 可提供文件摘要，内置工具暂未普遍发送更细粒度 output。
- 并发执行仅覆盖已标记安全的工具；Read/Write/Edit/Bash 仍串行，暂没有动态工具调度策略。
- provider/stream/context/max-iteration 等错误仍是 turn-level error。
- 已有 session resume:完整历史落盘到 `.runcode/sessions/`,`--resume`/`--continue` 跨进程恢复并通过 `InitialHistory` 注入;session id 同时用于 transcript 与 sessions 文件名。
- reasoning classification 是 prompt routing，不是 provider-native thinking。

### 工具系统

关键文件：

- `pkg/tool/tool.go`
- `pkg/tool/context.go`
- `pkg/tool/result.go`
- `pkg/tool/schema.go`
- `tools/registry.go`

当前注册内置工具：

1. `Read`
2. `Write`
3. `Edit`
4. `Glob`
5. `Grep`
6. `Bash`

`tools.Builtins()` 是当前工具可用性的单一注册源。它同时用于：

- `internal/repl.Executor` 实际执行工具。
- `internal/repl.ToolSpecs` 生成 provider tool specs。
- `internal/prompt/sections.UsingTools` 生成 prompt 中的工具说明。

最小化缺口：

- 内置工具静态注册；MCP 服务器工具已可动态接入（`mcp__<server>__<tool>`，stdio + HTTP），server 支持 resources 时再加 `ListMcpResources`/`ReadMcpResource` 两工具；skills 以渐进式披露接入（catalog 进 prompt + `Skill` 工具按需加载正文，约定目录发现）；plugin 与 workspace 配置动态工具尚未实现。
- executor 会通过 tool event channel 发送 started/completed/failed 生命周期事件，并为 ReadSet diff 附带安全文件摘要；Glob/Grep 会发送匹配文件摘要事件，但内置工具暂未普遍发送更细粒度 output。
- prompt 中只列工具 name 和 description，没有丰富 usage notes。
- 工具不会根据权限模式动态隐藏；safe 模式下 Write/Edit/Bash 仍会暴露给模型，但 prompt 会提示限制，运行时仍会被权限层拒绝。

## 当前工具状态

### Read

关键文件：`tools/read/read.go`

已实现效果：

- 读取 workspace 文件并返回带行号文本。
- 支持 `offset` / `limit`。
- 默认读取 2000 行。
- 有输出字节上限，超出标记 `[output truncated]`。
- 更新 `tool.Context.ReadSet`，记录 path、size、modtime、complete。
- 为 Write/Edit 的 fresh-read gate 提供基础。

缺口：

- 不支持图片、PDF、Notebook、多模态读取。
- 不做 binary 文件特殊处理。
- 不支持目录读取。
- 输出只是 text，没有结构化 metadata。

### Write

关键文件：`tools/write/write.go`

已实现效果：

- 创建新文件。
- 覆盖已有文件前要求 fresh complete read。
- 目标必须在 workspace 内。
- symlink escape 会被 mutation target 解析拦住。
- 不自动创建父目录。

缺口：

- 写入不是 atomic write。
- 覆盖时可能不保留原文件权限位。
- 没有 diff preview。
- 没有自动格式化或编码/换行保持。
- fresh-read 只用 size + modtime，不用 hash。

### Edit

关键文件：`tools/edit/edit.go`

已实现效果：

- exact `old_string` -> `new_string` 替换。
- 默认要求唯一匹配。
- `replace_all=true` 时替换全部。
- 要求 fresh complete read。
- 目标必须在 workspace 内。
- 有最大文件大小限制。

缺口：

- 不支持 append、insert、regex patch、line patch、unified diff patch。
- 不支持批量 edit transaction。
- 写回可能不保留原文件权限位。
- 没有内建 diff preview。

### Glob

关键文件：`tools/glob/glob.go`

已实现效果：

- workspace 内文件发现。
- 支持 slash-separated glob 和 `**` recursive segment。
- 输出 workspace-relative slash path。
- 支持 `path` 搜索根和 `limit`。
- 跳过 `.git`。
- 只返回文件。

缺口：

- 不读取 `.gitignore` / `.ignore`。
- 不是 fd/ripgrep 级性能。
- 没有 type filter、hidden filter、mtime sort。
- 常见大目录如 `node_modules` 未专门跳过。

### Grep

关键文件：`tools/grep/grep.go`

已实现效果：

- 使用 Go regexp 搜索 workspace 文本文件。
- 支持文件或目录搜索。
- 支持 `glob` 文件过滤、case-insensitive、limit。
- 输出 `relative/path:line:content`。
- 跳过二进制文件。
- 跳过 `.git`。

缺口：

- 不支持 ripgrep 完整语义。
- 不支持 before/after/context lines。
- 不支持 files-only、count、JSON output mode。
- 不支持 multiline。
- 不支持 type filter。
- 不读取 `.gitignore`。

### Bash

关键文件：`tools/bash/bash.go`

已实现效果：

- 执行单行 bash command。
- 使用 `exec.CommandContext(ctx, "bash", "-lc", command)`。
- cwd 固定为 workspace root。
- 不接 stdin。
- 默认 timeout 30s，最大 120s。
- stdout/stderr 各有 200 KiB 捕获上限。
- 返回 exit_code、timed_out、duration_ms、stdout、stderr、truncated。
- 非零 exit、timeout、cancel 返回 `tool.Result{IsError: true}`，不作为 executor Go error。

权限前置：

- 执行前先由 `internal/permissions` 对 command 分类和授权。
- safe 模式下不会执行 ask 命令。
- interactive 只审批非硬拒绝命令。
- unknown、privileged、outside-write、destructive VCS、complex shell-control 会 hard deny。

缺口：

- 没有 background task。
- 没有 streaming stdout/stderr。
- 没有 custom cwd/env/stdin。
- 没有 shell session 状态保持。
- 没有 sandbox/container/seccomp。
- 命令分类很保守，不是完整 shell parser。
- 管道、`;`、`&&`、`||`、backtick、`$()` 等复杂 shell control 会被拒绝。

## 权限系统状态

关键文件：

- `internal/permissions/action.go`
- `internal/permissions/resource.go`
- `internal/permissions/decision.go`
- `internal/permissions/resolver.go`
- `internal/permissions/policy.go`
- `internal/permissions/service.go`
- `internal/permissions/authorizer.go`
- `internal/permissions/approval.go`
- `internal/permissions/command.go`
- `internal/permissions/mutation.go`
- `internal/permissions/telemetry.go`

已实现效果：

- Resolver 将 tool input 解析成 `Action`。
- Policy 判断 allow / ask / deny。
- Authorizer 将 ask 转成最终 allow/deny。
- safe 模式中 ask 会由 NonInteractiveAuthorizer 转成 deny。
- interactive 模式中 ask 会调用 CLI approval prompt。
- hard deny 不会被 interactive approval 绕过。
- Read/Glob/Grep workspace 内 allow，workspace 外 deny。
- Write/Edit workspace 内合规 mutation ask，safe 下 deny，interactive 可审批。
- Write/Edit missing/partial/stale read、outside workspace、invalid target deny。
- Bash command 会分类为 command category/capability/risk reason/summary。
- Permission telemetry 只记录脱敏 metadata，不记录 raw path、raw command、tool input/output、file content、credential 或 URL。

缺口：

- 没有持久 allowlist / denylist。
- 已有进程内 session 级 permission memory（`MemorySessionAllowStore`）：interactive 下「allow for session」会记住等价操作（Write/Edit 按 mutation target，Bash 按命令分类），但不跨进程持久化。
- 没有 project/user/global policy config。
- 已有 allow once / allow session 两级选择；仍缺 allow project 与持久化。
- 没有 policy DSL。
- 没有组织策略或审计日志存储。
- permission denied 返回给模型的文本包含脱敏 reason/final effect；prompt 也会注入当前 permission mode 和关键权限约束。
- Bash 分类是保守浅解析，不是 shell AST。

## Prompt 系统状态

关键文件：

- `internal/prompt/assembler.go`
- `internal/prompt/boundary.go`
- `internal/prompt/sections/static.go`
- `internal/prompt/sections/dynamic.go`

已实现效果：

- `BuildSystemPrompt` 生成多个 `llm.ContentBlock`。
- 静态段包含 intro/system/tool descriptions/actions/tone。
- 动态段包含 reasoning guidance、cwd/date/shell info、permission mode guidance、memory、project context。
- `cmd/runcode chat` 会从 workspace 中首个命中的 `RUNCODE.md` / `CLAUDE.md` 加载 project context，并注入 `ProjectCtx`。
- 有 `__RUNCODE_DYNAMIC_BOUNDARY__` 作为静态/动态边界。
- 静态 block 使用 ephemeral cache control。
- 动态 block 不缓存。
- `sections.UsingTools` 从 `tools.Builtins()` 的 tool name/description 生成 prompt 工具说明。
- 可选 reasoning classifier 会选择受控 reasoning guidance。

缺口：

- project context loader 只读取第一个命中的 `RUNCODE.md` / `CLAUDE.md`，读取上限 64 KiB；不合并多个文件，也不支持 include。
- `Memory` 仍只是调用方传入字符串。
- 已有 TOML settings loader(`internal/persistence/settings`)供 CLI/TUI 配置;尚未用于 prompt memory 或 settings-backed policy guidance。
- 没有 prompt templates / go:embed。
- 没有 agent/skill prompt。
- 只注入固定 permission mode guidance，还没有更丰富的审批摘要或 settings-backed policy guidance。
- 如果未来工具集动态变化，cache boundary 需要重审。
- 没有 token budget / compaction。

## Transcript 状态

关键文件：

- `internal/persistence/transcript/store.go`
- `internal/persistence/transcript/jsonl.go`
- `internal/persistence/transcript/sanitize.go`
- `internal/persistence/transcript/session_id.go`
- `internal/repl/session.go`
- `cmd/runcode/chat.go`

已实现效果：

- 默认关闭，`--transcript jsonl` / `RUNCODE_TRANSCRIPT=jsonl` 显式开启。
- 支持 `--session-id` / `RUNCODE_SESSION_ID` 指定 transcript 文件名；未指定时自动生成 `sess_*`。
- 写入路径固定为 `<workspace>/.runcode/transcripts/<session-id>.jsonl`。
- 每个成功 turn 同步追加一条 JSONL 记录；失败 turn 不写。
- 记录 user text、final assistant text、stop reason、usage、iterations、tool call id/name、Bash command、tool result count/text byte summary。
- 不记录 system prompt、provider request、credential、base URL、普通工具 raw input、完整工具输出、thinking 内容或 image data。
- `/clear` 只清空内存 history，不删除或轮转 transcript。

缺口：

- transcript 不能用于 session resume。
- 没有 SQLite backend、索引、查询命令、rotation、compaction 或 migration。
- Bash command 字符串可能包含用户自己输入的 secret；后续可做 command redaction。

## Telemetry 状态

关键文件：

- `internal/telemetry/event.go`
- `internal/telemetry/recorder.go`
- `internal/telemetry/jsonl.go`
- `internal/telemetry/async.go`
- `internal/telemetry/memory.go`
- `internal/telemetry/id.go`
- `internal/repl/telemetry.go`
- `internal/permissions/telemetry.go`

已实现效果：

- 统一 event model。
- 事件覆盖 turn、LLM request、tool execute、permission decision。
- 有 trace/turn/request/tool_use correlation IDs。
- Noop recorder 默认无行为。
- JSONL recorder 可写 stderr。
- Async recorder 有 bounded queue，满了 drop，不阻塞主链路。
- Memory recorder 用于测试。
- permission telemetry 记录脱敏摘要。
- tool error telemetry 使用受控错误类别，不记录 raw path。

缺口：

- 没有 OpenTelemetry exporter。
- 没有 telemetry persistence。
- 没有 sampling。
- 没有 session/user/install 聚合 ID。
- 没有 schema version。
- LLM request error 当前仍可能记录 provider error string，后续需要错误分类/脱敏。

## 文档状态

相对可信：

- `README.md` / `README.zh-CN.md`：已反映当前 `chat`、`chat --loop`、TUI MVP、权限、telemetry、transcript 和内置工具状态。
- `docs/architecture.md`：当前架构说明较新。
- `docs/data-flow-and-prompt.md`：已覆盖当前 CLI / TUI / Session / tool / permission / prompt 数据流。
- `CHANGELOG.md`：已记录近期 telemetry、permissions、transcript、history trimming、streaming chat 和 TUI MVP。
- `docs/session-handoff.md`：历史日志，最新段落可信，早期段落可能过期。

仍需后续补充：

- 更完整的 TUI 产品文档，例如 permission modal、rich tool output 和 transcript resume 设计。
- settings、MCP、hooks、skills、sub-agents 等后续模块落地后的独立说明。

## 空壳与未实现目录

以下目录主要仍是 `.gitkeep` 或没有实质实现：

- `internal/app/components`
- `internal/persistence/claudemd`
- `internal/persistence/sqlite`
- `internal/persistence/migrate`
- `internal/coordinator`
- `internal/session`
- `internal/cost`
- `internal/hooks`
- `pkg/agent`
- `pkg/command`
- `pkg/plugin`
- `prompts/templates`
- `prompts/agents`
- `prompts/skills`（skills 已实现，但从约定目录发现，不内置示例 skill）
- `examples/custom-tool`

对应未实现能力：

- 完整 TUI 产品能力：diff viewer、transcript browser、文件树、语法高亮(permission modal、rich tool output、多行输入和 `/model` 运行时切换已实现)。
- SQLite transcript backend。
- 配置写入命令 / settings-backed 权限策略持久化(TOML 配置读取已实现)。
- 更细的 cost tracking（按模型定价表自动估算）。当前已实现 token 累计 + `/cost` + 手动单价（`--input-price`/`--output-price`/`input_price`/`output_price`）；`internal/cost` 包仍是空壳。
- hooks。
- MCP prompts / sampling / roots（tools 与 resources 原语已实现：stdio + Streamable HTTP，作为 external 操作需审批；server 仅用户级配置；resources 经 `ListMcpResources`/`ReadMcpResource` 两工具按需访问，仅在 server 声明 resources 能力时出现）。
- sub-agents。
- slash commands。
- plugins。
- 可执行 skill / 语义匹配 / 远程 skill 仓库（指令型 skills 已实现：约定目录发现 + 渐进式披露 + `Skill` 工具按需加载，作为 manage 操作免审批）。
- custom tool example。

## 主要缺口按优先级

### 1. Session resume 与 compaction(已实现)

已落地:独立的完整-history 存储 `internal/persistence/sessions`(`.runcode/sessions/<id>.jsonl`,默认开启),`--resume`/`--continue` 跨进程恢复并通过 `SessionOptions.InitialHistory` 注入;`internal/compaction` 在 `--max-context-tokens` 设置且某轮 input tokens 接近预算时,调 LLM 把最旧 turn 总结成一条摘要消息,保留最近 turn。磁盘 append-only 完整、内存工作集可压缩。

后续仍可做:transcript/session 浏览检索界面、SQLite 后端、按模型自动推断上下文窗口、`/clear` 轮转独立 session 文件(当前 `/clear` 仅清内存、磁盘保持完整日志)、图像大块外部化。

### 2. 模型可自我修正能力

当前 permission denied、unknown tool 和普通工具 runtime error 都会作为 `is_error=true` tool_result 回灌模型；provider/stream/context/max-iteration 等仍作为 turn-level error。

### 3. Prompt 上下文落地

`ProjectCtx` 已由 `RUNCODE.md` / `CLAUDE.md` loader 接入，但 prompt 上下文仍缺少：

- settings loader。
- memory loader。
- 更丰富的 permission summary 注入 prompt。

### 4. CLI / TUI 交互体验

目前已有 shell-friendly `chat`、stdout assistant streaming 和最小 TUI MVP，但还不是产品级终端体验。缺少：

- 多行输入与 readline 式历史翻阅已实现；仍缺命令补全和可配置快捷键。
- slash 命令 `/mode`、`/model`、`/compact`、`/cost` 已实现；后续可加更多。
- 更丰富的 tool output display。
- TUI permission modal 和更丰富 approval 选项。

### 6. 权限策略持久化（部分实现）

已实现：allow once、进程内 allow for session，以及跨进程持久化的 **allow for project**
（`<workspace>/.runcode/permissions.json`，0600，gitignore）+ **denylist**（询问前检查、deny
始终优先于 allow、文件损坏时快速报错而非静默丢弃 deny 规则）。CLI prompt（`[p]roject`）与 TUI
modal（`[p] allow project`）均可选。grain 与 session 一致（Write/Edit 按 mutation target、Bash 按
命令分类）。键格式集中在 `NetworkSessionKey`/`MutateSessionKey`/`CommandSessionKey` 构造器
与 `ParseRule` 读取器（运行时与 CLI 共用同一来源）。`runcode permissions` 管理命令已实现：
`list` 按编号可读列出 allow/deny（含 TUI 写入的 mutation/command 规则）、`remove <n>` 按编号删除
任意规则、`clear [--allow|--deny]` 清空、`deny/allow <host>` 按 host 增删网络规则（唯一可手输且
精确匹配的键类型）。

后续仍可做：

- user 级（全局）allow/deny。
- mutation/command 规则的 CLI 录入（目前只支持 network host 的 add；其它键含绝对路径/能力集，
  手输难以精确匹配，只支持 list/remove）。

### 7. 工具增强

可选方向：

- Grep context lines / files-only / count / `.gitignore`。
- Edit append/insert/regex/line patch。
- Write atomic write + preserve mode。
- Bash streaming/background task。
- WebSearch（需配置搜索 API provider）。`WebFetch` 已实现：抓取 http(s) URL → 纯文本，作为
  `network` 操作需审批（按 host 显示与记忆）。

### 8. Provider 扩展

可选方向：

- **Provider capabilities 接线（部分实现）**：`SupportsCacheControl` 已接线——`repl.NewSession`
  从 provider 读取该能力并注入 prompt assembler，static system-prompt 段仅在 provider 支持时带
  ephemeral cache hint（Anthropic=true 保留缓存优化；OpenAI=false 不再生成无用 cache 字段）。
  仍未接线：(1) 按 provider 上下文窗口自动推断压缩预算——`Capabilities().MaxContextTokens` 当前与
  CLI 的 `--max-context-tokens` 同源，要有意义需 provider 提供独立来源（model→窗口映射或 API 查询），
  对任意 model 名的 OpenAI 兼容端点不可行，故留作进一步工作；(3) `SupportsThinking` 暂无消费方。
- Anthropic image support。
- stop sequences / tool choice / thinking budget。

> retry/backoff（network/429/5xx/529，capped 指数退避 + 尊重 `Retry-After`，仅连接建立阶段）已实现：
> OpenAI 在 HTTP transport 内重试，Anthropic 启用 SDK 内置重试。重试次数可配
> （`--max-retries` / `RUNCODE_MAX_RETRIES` / `max_retries`，0=默认、负=禁用）。

## 后续推荐路线

下列基础能力已落地,把最小闭环推进到“更像可长期使用的开发助手”:

1. ✅ session resume + context compaction(完整历史持久化 + `--resume`/`--continue` + token 预算压缩)。
2. ✅ TUI permission modal + 会话级权限记忆 + rich tool output(输出摘要 + Edit/Write 行级 diff)。
3. ✅ TOML 配置文件加载(项目级 + 用户级,`runcode config` 查看)。
4. ✅ 会话恢复(`--resume`/`--continue`)+ 增量上下文压缩(摘要逐字保留,不重复重总结)。
5. ✅ OpenAI 兼容 provider(`--provider openai`,直连 Chat Completions HTTP/SSE,支持各类兼容端点)。

6. ✅ skills(约定目录发现 + 渐进式披露:catalog 进 prompt + `Skill` 工具按需加载正文,项目级生效并标注来源)。

后续可选大方向:hooks/sub-agents、WebSearch、MCP 的 prompts/sampling/roots、可执行 skill。
