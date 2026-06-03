# runcode 执行数据流与提示词拼接结构

本文档基于当前代码实现整理，用于快速理解 `runcode` 的 CLI 执行链路、ReAct 工具数据流、权限边界、telemetry 和 system prompt 拼接方式。

当前范围是 v0.1-alpha 的最小可运行闭环：`cmd/runcode chat` 已接入 Anthropic provider、`internal/repl.Session`、内置工具、权限系统和 telemetry；`cmd/runcode tui` 已接入最小 Bubble Tea TUI MVP；但还没有完整 TUI 权限弹窗、持久 transcript resume、context compaction、MCP、hooks、sub-agents、skills 或 OpenAI provider。

## 1. CLI 到 Session 的数据流

```text
用户输入
  -> cmd/runcode chat OR cmd/runcode tui
  -> shared chatConfig/session factory
  -> anthropic.New(provider options)
  -> tools.Builtins()
  -> permissions.Service
  -> repl.NewSession
  -> Session.RunTurn
  -> chat stdout OR TUI StreamDelta / ToolEvents
```

当前 CLI 入口：

- `cmd/runcode/main.go`
- `cmd/runcode/chat.go`
- `cmd/runcode/tui.go`
- `cmd/runcode/line_input.go`
- `internal/ui/*`
- `cmd/runcode/approval.go`

实际效果：

- `runcode chat [prompt]` 从 args 或 stdin 读取 prompt。
- `runcode chat --loop` 逐行读取 stdin，并复用同一个内存 session；`/clear` 会清空该 session 的内存 history。
- `runcode tui` 启动最小 Bubble Tea TUI，复用同一套 chat 配置和 session 构造路径，把 `StreamDelta` 和 `ToolEvents` 转换成 UI event 后渲染；界面包含 Claude Code 风格底部状态区、累计上下文 token 与思考模式指示、上下分隔线包裹的单行输入、assistant Markdown 和树状工具进度。
- `--max-history-messages` / `RUNCODE_MAX_HISTORY_MESSAGES` 限制每轮发送给 provider 的内存 history 消息数（`0` = 不限制，默认）。
- `--provider` 当前只支持 `anthropic`。
- `--model` / `ANTHROPIC_MODEL` 必填。
- `--api-key` / `ANTHROPIC_API_KEY` 或 `--auth-token` / `ANTHROPIC_AUTH_TOKEN` 必填其一。
- `--cwd` / `RUNCODE_CWD` 决定工具工作目录。
- `--permission-mode safe|interactive` 控制审批模式。
- `--telemetry off|jsonl` 控制 telemetry 输出。
- `--transcript off|jsonl` / `RUNCODE_TRANSCRIPT` 控制是否写入 JSONL transcript。
- `--session-id` / `RUNCODE_SESSION_ID` 可指定 transcript 文件名。
- 配置解析在 `chatConfigFromCommand`(chat 与 tui 共用)内完成,优先级 flag > env > 项目 `runcode.toml` > 用户 `config.toml` > 默认;凭证仅取自用户级文件。`runcode config` 打印生效配置与命中路径(凭证脱敏)。

当前限制：

- `chat` 保持 shell-friendly 输出，并把 assistant text delta 实时写到 stdout；TUI 负责交互式流式渲染。
- `--loop` 不是完整 REPL，除 `/clear` / exit aliases 外没有完整 slash command 系统、readline、多行输入或 transcript-backed session resume。
- TUI 仍是 MVP：已有 Claude Code 风格底部状态区、累计上下文 token 与思考模式指示、上下分隔线包裹的单行输入、带安全文件摘要的树状 tool progress cards，以及 interactive 模式下的 permission modal（allow once / allow session / deny）、会话级权限记忆，以及 rich tool output（脱敏输出摘要 + Edit/Write 行级 diff，`ctrl+o` 展开）；但仍没有文件树、syntax highlighting 或 transcript-backed session resume。

## 2. Session RunTurn 数据流

```text
Session.RunTurn(ctx, userText)
  -> clone in-memory history
  -> append current user message
  -> optional reasoning classification
  -> trimMessagesForHistoryBudget (按 message-count budget 裁剪旧历史)
  -> buildRequestWithMessagesAndPrompt
  -> provider.Stream
  -> collectAssistantMessage
  -> if no tool_use: commit history, optionally write transcript, and return final text
  -> if tool_use: executeToolUses
  -> append assistant + tool_result messages
  -> repeat until no tool_use or max iterations
```

关键文件：

- `internal/repl/session.go`
- `internal/repl/reasoning.go`
- `internal/repl/tool_result.go`
- `internal/repl/toolspec.go`
- `internal/repl/telemetry.go`

核心行为：

- Session 持有进程内 `[]llm.Message` history。
- 成功 turn 会提交 history，并在开启 transcript 时写入一条 JSONL turn summary；失败 turn 不提交、不写 transcript。
- 开启 `MaxHistoryMessages` 后，发送给 provider 和提交回内存的 history 都按 message-count budget 裁剪：保留最新若干完整 turn，永不裁掉当前 turn，也绝不拆散 `tool_use`/`tool_result` 配对；`0` 表示不裁剪。裁剪只作用于内存/请求 history，不触碰 transcript 文件。
- `History()` 返回 clone，避免外部修改内部状态。
- `ResetHistory()` 清空当前进程内 history。
- 每个 turn 最多执行 `MaxIterations` 次 provider/tool 循环，默认 8。
- 可选 reasoning classification 会在主请求前发一次小请求，用受控 scenario 选择 reasoning guidance。
- classification 请求不进入主 conversation history。

当前限制：

- transcript 是 append-only 摘要记录，当前还不能恢复 session history。
- 已有按 message-count 的 history budget（默认关闭），但没有 token 精确预算、语义 context compaction 或 transcript-backed resume。
- 连续的 concurrency-safe tool_use 会批量并发执行；当前只有 `Glob` 和 `Grep` 标记为并发安全，interactive approval 可用时会退回串行执行。
- permission denied、unknown tool 和普通工具 runtime error 会作为 `is_error=true` tool_result 回灌模型；provider/stream/context/max-iteration 等仍会中断 turn。

## 3. Prompt 拼接结构

入口：

- `internal/prompt/assembler.go`

相关文件：

- `internal/prompt/boundary.go`
- `internal/prompt/sections/static.go`
- `internal/prompt/sections/dynamic.go`

`BuildSystemPrompt(opts)` 输出 `[]llm.ContentBlock`，而不是单个字符串。

静态 section：

1. intro
2. system behavior
3. built-in tool descriptions
4. action guidance
5. tone/style guidance
6. `__RUNCODE_DYNAMIC_BOUNDARY__`

动态 section：

1. optional reasoning guidance
2. current working directory
3. current date
4. shell info
5. permission mode guidance
6. memory text
7. project context text

Cache 策略：

- 静态 section 使用 `llm.CacheControlEphemeral`。
- 动态 section 使用 `llm.CacheControlNone`。
- 当前工具集来自静态 `tools.Builtins()`，因此工具描述仍放在静态缓存区。

当前限制：

- `ProjectCtx` 由 `cmd/runcode chat` 从 workspace 中首个命中的 `RUNCODE.md` / `CLAUDE.md` 加载，读取上限 64 KiB，超出会截断。
- `Memory` 仍只是调用方传入字符串。
- 没有 settings loader。
- 没有 prompt template embed。
- 没有 agent/skill prompt。
- 当前 permission mode 和关键权限约束会注入动态 prompt，但工具 spec 仍不会按 permission mode 动态隐藏。
- 未来 MCP/plugin 动态工具接入后，需要重新评估工具描述的 cache boundary。

## 4. 工具注册与 tool spec 数据流

注册源：

- `tools/registry.go`

当前 `tools.Builtins()` 返回：

```text
Read
Write
Edit
Glob
Grep
Bash
```

这些工具同时流向三个地方：

```text
tools.Builtins()
  -> repl.NewExecutor -> Executor.Execute -> Tool.Run
  -> repl.ToolSpecs -> llm.Request.Tools -> provider tool schema
  -> prompt.BuildSystemPrompt -> sections.UsingTools -> system prompt text
```

这样能保持 prompt 可见工具、provider tool spec 和 executor 实际工具列表来自同一个注册源。

当前限制：

- 工具是静态注册，没有 MCP、plugin 或配置驱动的动态工具。
- Prompt 中只列 tool name 和 description，不列完整 schema 或详细 usage notes。
- 工具不会根据 permission mode 动态隐藏；safe 模式下 Write/Edit/Bash 仍暴露给模型，但动态 prompt 会提示这些动作会被拒绝，执行时仍由权限层兜底。

## 5. 工具执行与权限数据流

执行入口：

- `internal/repl/executor.go`

数据流：

```text
Executor.Execute(ctx, req)
  -> find tool runner by req.Name
  -> prepare tool.Context and ToolUseID
  -> permissions.Service.AuthorizeTool
  -> permissions.RecordDecision
  -> if denied: emit failed ToolEvent and return is_error tool result with sanitized reason without running tool
  -> if unknown tool: emit failed ToolEvent and return is_error tool result
  -> emit started ToolEvent
  -> record tool.execute.start
  -> runner.Run
  -> forward tool progress/output events with ToolName + ToolUseID
  -> attach typed safe file references for ReadSet / Glob / Grep summaries
  -> if recoverable tool error: emit failed ToolEvent and return is_error tool result
  -> record tool.execute.end or tool.execute.error
  -> emit completed or failed ToolEvent
```

权限层：

- `internal/permissions/resolver.go`
- `internal/permissions/policy.go`
- `internal/permissions/authorizer.go`
- `internal/permissions/approval.go`
- `internal/permissions/command.go`
- `internal/permissions/telemetry.go`

当前 policy 行为：

- `Read` / `Glob` / `Grep`：workspace 内 allow，workspace 外 deny。
- `Write`：workspace 内 create/fresh overwrite ask；safe 下最终 deny；interactive 可审批。
- `Edit`：workspace 内 fresh exact replace ask；safe 下最终 deny；interactive 可审批。
- `Bash`：先做命令分类；非硬拒绝命令 ask；safe 下最终 deny；interactive 可审批；unknown/privileged/destructive/outside-write/complex shell-control hard deny。

审批与 telemetry：

- `interactive` 模式通过 CLI stderr prompt 询问 allow once / deny。
- hard deny 不会进入审批。
- Permission telemetry 只记录受控摘要，不记录 raw path、raw command、tool input/output、file content、credential 或 URL。

## 6. 内置工具效果

### Read

文件：`tools/read/read.go`

效果：读取 workspace 文件，返回带行号文本，并更新 `tool.Context.ReadSet`。支持 offset/limit 和输出截断。

缺口：不支持图片、PDF、Notebook、多模态读取或目录读取。

### Write

文件：`tools/write/write.go`

效果：创建文件；覆盖已有文件前要求 fresh complete read；目标必须在 workspace 内。

缺口：非原子写入，不保留原文件权限位，不创建父目录，没有 diff preview。

### Edit

文件：`tools/edit/edit.go`

效果：对 fresh-read 文件做 exact string replacement；默认要求唯一匹配，`replace_all=true` 替换全部。

缺口：不支持 append、insert、regex patch、line patch、unified diff patch 或批量 edit transaction。

### Glob

文件：`tools/glob/glob.go`

效果：用 slash glob 和 `**` 搜索 workspace 文件，输出 workspace-relative slash path。

缺口：不读取 `.gitignore`，不支持 type filter、hidden filter、mtime sort。

### Grep

文件：`tools/grep/grep.go`

效果：用 Go regexp 搜索 workspace 文本文件，支持 path、glob、case_insensitive、limit，输出 `relative/path:line:content`。

缺口：不支持 ripgrep 完整语义、context lines、files-only、count、JSON output、multiline 或 type filter。

### Bash

文件：`tools/bash/bash.go`

效果：在 workspace root 执行单行非交互 Bash 命令；有 timeout、stdout/stderr 捕获、输出截断；非零 exit/timeout/cancel 返回 `IsError` tool result。

缺口：不支持 background task、streaming output、stdin、自定义 cwd/env、shell session、sandbox 或完整 shell parser。

## 7. LLM provider 数据流

中立层：

- `pkg/llm/provider.go`
- `pkg/llm/message.go`
- `pkg/llm/stream.go`

Anthropic provider：

- `pkg/llm/providers/anthropic/provider.go`
- `pkg/llm/providers/anthropic/sdk.go`
- `pkg/llm/providers/anthropic/convert.go`
- `pkg/llm/providers/anthropic/stream.go`

当前效果：

- 使用官方 Anthropic Go SDK streaming API。
- 转换 system/messages/tools/max tokens/temperature。
- 转换 text、tool_use、tool_result、thinking block。
- 转换 text delta、partial JSON delta、thinking delta、signature delta、usage、stop reason。
- 支持 API key、auth token、base URL。

当前限制：

- 只实现 Anthropic provider。
- OpenAI provider 仍为空壳。
- 不支持 image block 转换。
- 不支持 stop sequences、tool choice、top_p/top_k、thinking budget、metadata 透传、retry/backoff 或 non-streaming API。

## 8. Transcript 数据流

入口：

- `internal/persistence/transcript`
- `internal/repl.Session.RunTurn`
- `cmd/runcode chat --transcript jsonl`

当前效果：

- 默认关闭，`--transcript jsonl` 或 `RUNCODE_TRANSCRIPT=jsonl` 开启。
- 写入 `<workspace>/.runcode/transcripts/<session-id>.jsonl`。
- `--session-id` / `RUNCODE_SESSION_ID` 可指定 session id；未指定时自动生成。
- 每个成功 turn 写一条 JSONL 记录；失败 turn 不写。
- 记录 user text、final assistant text、stop reason、usage、iterations、tool call id/name、Bash command、tool result 计数和 text bytes。
- 不记录 system prompt、provider request、credential、base URL、普通工具 raw input、完整工具输出、thinking 内容或 image data。
- `/clear` 只清空内存 history，不删除或轮转 transcript。

当前限制：

- transcript 不能用于 session resume。
- 没有 SQLite、索引、查询命令、compaction 或 rotation。

## 9. Telemetry 数据流

关键文件：

- `internal/telemetry/event.go`
- `internal/telemetry/recorder.go`
- `internal/telemetry/jsonl.go`
- `internal/telemetry/async.go`
- `internal/telemetry/memory.go`
- `internal/telemetry/id.go`
- `internal/repl/telemetry.go`
- `internal/permissions/telemetry.go`

事件类型：

- `turn.start`
- `turn.end`
- `turn.error`
- `llm.request.start`
- `llm.request.end`
- `llm.request.error`
- `tool.execute.start`
- `tool.execute.end`
- `tool.execute.error`
- `permission.decision`

当前效果：

- 默认 noop recorder。
- `--telemetry jsonl` 使用 bounded async recorder 输出到 stderr。
- 事件带 trace/turn/request/tool_use IDs。
- Permission telemetry 和 tool error telemetry 避免记录 raw input/output/path。

缺口：

- 没有 OpenTelemetry exporter。
- 没有 telemetry persistence。
- 没有 sampling。
- 没有 schema version。
- `llm.request.error` 仍可能包含 provider error string，后续需要错误分类/脱敏。

## 10. 仍未实现的大模块

主要空壳或未实现目录：

```text
internal/app/components
internal/persistence/claudemd
internal/persistence/settings
internal/persistence/sqlite
internal/persistence/migrate
internal/coordinator
internal/session
internal/compaction
internal/cost
internal/hooks
internal/mcp
pkg/llm/providers/openai
pkg/agent
pkg/command
pkg/plugin
pkg/skill
tools/todo
prompts/templates
prompts/agents
prompts/skills
examples/custom-tool
```

对应未实现能力：

- 完整 TUI 产品能力：permission modal、rich tool output、diff viewer、transcript browser、多行输入和 model switching。
- SQLite transcript backend 和 session resume。
- settings persistence。
- context compaction。
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

## 11. 当前最应该补的缺口

如果目标是减少半成品感，而不是继续扩新功能，建议顺序是：

1. 实现 transcript-backed session resume 或 context compaction。
2. 再考虑 TodoWrite、rich tool output、完整 TUI 产品能力、MCP 等更大功能。
