# runcode 当前实现状态与缺口

日期：2026-05-27

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

- 最小 CLI：`version`、`chat`、`chat --loop`。
- Provider-neutral LLM 抽象。
- Anthropic streaming provider。
- 有限 ReAct session loop。
- 进程内多轮 history。
- 内置工具：`Read`、`Write`、`Edit`、`Glob`、`Grep`、`Bash`。
- 统一权限系统：safe / interactive、approval、permission telemetry。
- prompt assembler 与静态/动态 cache boundary。
- telemetry event model、JSONL stderr 输出、async recorder。

但整体仍是 `v0.1-alpha` 最小实现。很多目录仍是空壳，很多能力只做到安全可验证的第一版，没有产品级交互体验、持久化、配置系统、TUI、MCP、hooks、skills、sub-agents、context compaction 或完整多 provider 支持。

## 当前已实现模块

### CLI

关键文件：

- `cmd/runcode/main.go`
- `cmd/runcode/chat.go`
- `cmd/runcode/line_input.go`
- `cmd/runcode/approval.go`

已实现效果：

- `runcode version` 输出版本、commit、build time、Go 平台信息。
- `runcode chat [prompt]` 可从 args 或 stdin 读取 prompt。
- `runcode chat --loop` 可在同一进程中逐行对话并复用一个 session，`/clear` 可清空该 session 的内存 history。
- `--provider` 目前只支持 `anthropic`。
- 支持 model、max tokens、base URL、API key、auth token、cwd、telemetry、permission mode 配置。
- 支持环境变量：`RUNCODE_PROVIDER`、`ANTHROPIC_MODEL`、`ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_BASE_URL`、`ANTHROPIC_MAX_TOKENS`、`RUNCODE_CWD`、`RUNCODE_TELEMETRY`、`RUNCODE_PERMISSION_MODE`。
- `--telemetry jsonl` 会把 telemetry event 以 JSONL 写到 stderr。
- `--permission-mode interactive` / `confirm` 会在 stderr 提示一次性审批。
- approval prompt 只显示脱敏摘要，不显示 raw path、raw command、file content、credential 或 URL。

最小化缺口：

- `chat --loop` 只是逐行循环，不是完整 REPL/TUI。
- 没有 readline、历史导航、多行输入、补全、快捷键。
- 没有完整 slash command 系统，例如 `/help`、`/compact`、`/model`。
- 没有实时 token streaming 输出，当前只在一轮完成后打印 final text。
- 没有 tool progress UI。
- 没有配置文件系统。
- 没有 session 持久化或恢复。
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

- OpenAI provider 目录仍为空壳。
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
- 成功 turn 会提交 history；失败 turn 不提交，避免污染后续上下文。
- 支持 `History()` clone 和 `ResetHistory()`。
- 支持 optional reasoning classification，将用户任务归类后注入动态 reasoning guidance。
- `Executor` 在任何工具运行前统一调用 `permissions.Service.AuthorizeTool`。
- permission denied 被转换成带脱敏 reason/final effect 的 `is_error=true` tool result 回传模型。
- unknown tool 和普通工具 runtime error 会转换成 `is_error=true` tool result，让模型有机会自我修正。
- 工具执行 telemetry 和 permission telemetry 已接入。

最小化缺口：

- session history 只在内存中，不持久化。
- 没有 context compaction 或 token budget trimming。
- 没有实时 streaming observer 给 CLI/UI 展示 token。
- tool_use 当前顺序执行，没有并发工具执行策略。
- provider/stream/context/max-iteration 等错误仍是 turn-level error。
- 没有 session resume、session id、transcript store。
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

- 工具是静态注册，没有 MCP、plugin 或 workspace 配置动态工具。
- tool event channel 基本没有被实际工具使用。
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
- 没有 session 级 permission memory。
- 没有 project/user/global policy config。
- 没有 allow once/session/project 的多级选择。
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
- 没有 settings loader。
- 没有 prompt templates / go:embed。
- 没有 agent/skill prompt。
- 只注入固定 permission mode guidance，还没有更丰富的审批摘要或 settings-backed policy guidance。
- 如果未来工具集动态变化，cache boundary 需要重审。
- 没有 token budget / compaction。

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

- `docs/architecture.md`：当前架构说明较新。
- `docs/session-handoff.md`：历史日志，最新段落可信，早期段落已过期。

存在明显过期：

- `docs/data-flow-and-prompt.md`：仍有“CLI 尚未接入 Session”“工具只有 Read”等旧描述。
- `README.md`：仍描述 chat 未接通或 placeholder 状态。
- `README.zh-CN.md`：同上。
- `CHANGELOG.md`：没有反映近期 telemetry、permissions、Write/Edit/Glob/Grep/Bash、chat loop 等实现。

建议：后续应优先更新这些对外文档，避免误导。

## 空壳与未实现目录

以下目录主要仍是 `.gitkeep` 或没有实质实现：

- `internal/app/components`
- `internal/ui`
- `internal/persistence/claudemd`
- `internal/persistence/settings`
- `internal/persistence/sqlite`
- `internal/persistence/transcript`
- `internal/persistence/migrate`
- `internal/coordinator`
- `internal/session`
- `internal/compaction`
- `internal/cost`
- `internal/hooks`
- `internal/mcp`
- `pkg/llm/providers/openai`
- `pkg/agent`
- `pkg/command`
- `pkg/plugin`
- `pkg/skill`
- `tools/todo`
- `prompts/templates`
- `prompts/agents`
- `prompts/skills`
- `examples/custom-tool`

对应未实现能力：

- Bubble Tea TUI。
- SQLite transcript。
- settings 持久化。
- session persistence。
- compaction。
- cost tracking。
- hooks。
- MCP。
- sub-agents。
- slash commands。
- plugins。
- skills。
- OpenAI provider。
- TodoWrite。
- custom tool example。

## 主要缺口按优先级

### 1. 文档同步

当前 README 和 `docs/data-flow-and-prompt.md` 与代码实际状态不一致。应先修正事实源：

- `docs/data-flow-and-prompt.md`
- `README.md`
- `README.zh-CN.md`
- `CHANGELOG.md`

### 2. 模型可自我修正能力

当前 permission denied、unknown tool 和普通工具 runtime error 都会作为 `is_error=true` tool_result 回灌模型；provider/stream/context/max-iteration 等仍作为 turn-level error。

### 3. Prompt 上下文落地

`ProjectCtx` 已由 `RUNCODE.md` / `CLAUDE.md` loader 接入，但 prompt 上下文仍缺少：

- settings loader。
- memory loader。
- 更丰富的 permission summary 注入 prompt。

### 4. 会话持久化与 compaction

当前 `chat --loop` 有内存 history，但没有保存、恢复或压缩。建议实现：

- transcript store。
- session id。
- session resume 或 transcript-backed history 管理。
- context compaction。

### 5. CLI 交互体验

目前是最小 shell-friendly CLI，不是产品级终端体验。缺少：

- streaming output。
- readline。
- slash commands。
- tool progress display。
- approval 更丰富选项。

### 6. 权限策略持久化

当前 interactive 只有 allow once。缺少：

- allow for session。
- allow project。
- denylist。
- settings-backed policy。

### 7. 工具增强

可选方向：

- TodoWrite。
- Grep context lines / files-only / count / `.gitignore`。
- Edit append/insert/regex/line patch。
- Write atomic write + preserve mode。
- Bash streaming/background task。

### 8. Provider 扩展

可选方向：

- OpenAI provider。
- Anthropic image support。
- stop sequences / tool choice / thinking budget。
- retry/backoff/rate-limit 分类。

## 后续推荐路线

如果目标是减少“半成品感”，建议不要马上继续堆新大功能，而是先补三个基础缺口：

1. 更新过期文档，让外部说明与代码一致。
2. 增加 `/clear` 或最小 history reset 入口，补齐当前内存会话的基础控制能力。
3. 再推进 session persistence、context compaction 或更完整的 CLI 交互体验。

这三项不会显著扩大架构面，但能把当前最小闭环从“能跑”推进到“更像可用的开发助手”。
