# runcode 执行数据流与提示词拼接结构

本文档基于当前代码实现整理，用于快速理解 `runcode` 的 CLI 执行链路、ReAct 工具数据流、权限边界、telemetry 和 system prompt 拼接方式。

当前范围是 v0.1-alpha 的最小可运行闭环：`cmd/runcode chat` 已接入 Anthropic provider、`internal/repl.Session`、内置工具、权限系统和 telemetry；但还没有 Bubble Tea TUI、持久 transcript、context compaction、MCP、hooks、sub-agents、skills 或 OpenAI provider。

## 1. CLI 到 Session 的数据流

```text
用户输入
  -> cmd/runcode chat
  -> defaultChatRunner.Run
  -> defaultChatRunner.sessionFor
  -> anthropic.New(provider options)
  -> tools.Builtins()
  -> permissions.Service
  -> repl.NewSession
  -> Session.RunTurn
```

当前 CLI 入口：

- `cmd/runcode/main.go`
- `cmd/runcode/chat.go`
- `cmd/runcode/line_input.go`
- `cmd/runcode/approval.go`

实际效果：

- `runcode chat [prompt]` 从 args 或 stdin 读取 prompt。
- `runcode chat --loop` 逐行读取 stdin，并复用同一个内存 session。
- `--provider` 当前只支持 `anthropic`。
- `--model` / `ANTHROPIC_MODEL` 必填。
- `--api-key` / `ANTHROPIC_API_KEY` 或 `--auth-token` / `ANTHROPIC_AUTH_TOKEN` 必填其一。
- `--cwd` / `RUNCODE_CWD` 决定工具工作目录。
- `--permission-mode safe|interactive` 控制审批模式。
- `--telemetry off|jsonl` 控制 telemetry 输出。

当前限制：

- CLI 不做 token 实时渲染；每轮完成后输出 final assistant text。
- `--loop` 不是完整 REPL，没有 readline、slash commands、多行输入或 session persistence。
- 没有 TUI。

## 2. Session RunTurn 数据流

```text
Session.RunTurn(ctx, userText)
  -> clone in-memory history
  -> append current user message
  -> optional reasoning classification
  -> buildRequestWithMessagesAndPrompt
  -> provider.Stream
  -> collectAssistantMessage
  -> if no tool_use: commit history and return final text
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
- 成功 turn 会提交 history；失败 turn 不提交。
- `History()` 返回 clone，避免外部修改内部状态。
- `ResetHistory()` 清空当前进程内 history。
- 每个 turn 最多执行 `MaxIterations` 次 provider/tool 循环，默认 8。
- 可选 reasoning classification 会在主请求前发一次小请求，用受控 scenario 选择 reasoning guidance。
- classification 请求不进入主 conversation history。

当前限制：

- history 不持久化。
- 没有 context compaction 或 token budget trimming。
- tool_use 顺序执行，不做并发工具调度。
- 工具 runtime error 和 unknown tool 会中断 turn；permission denied 则作为 tool_result 回灌模型。

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
5. memory text
6. project context text

Cache 策略：

- 静态 section 使用 `llm.CacheControlEphemeral`。
- 动态 section 使用 `llm.CacheControlNone`。
- 当前工具集来自静态 `tools.Builtins()`，因此工具描述仍放在静态缓存区。

当前限制：

- `Memory` 和 `ProjectCtx` 只是调用方传入字符串。
- 还没有磁盘上的 `RUNCODE.md` / `CLAUDE.md` loader。
- 没有 settings loader。
- 没有 prompt template embed。
- 没有 agent/skill prompt。
- 没有把当前 permission mode 明确注入 prompt。
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
- 工具不会根据 permission mode 动态隐藏；safe 模式下 Write/Edit/Bash 仍暴露给模型，但执行时会被权限拒绝。

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
  -> if denied: return is_error tool result without running tool
  -> record tool.execute.start
  -> runner.Run
  -> record tool.execute.end or tool.execute.error
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

## 8. Telemetry 数据流

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

## 9. 仍未实现的大模块

主要空壳或未实现目录：

```text
internal/app/components
internal/ui
internal/persistence/*
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

- Bubble Tea TUI。
- SQLite transcript 和 session persistence。
- `RUNCODE.md` / `CLAUDE.md` loader。
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

## 10. 当前最应该补的缺口

如果目标是减少半成品感，而不是继续扩新功能，建议顺序是：

1. 让工具 runtime error / unknown tool 也能以 `is_error=true` tool result 回灌模型。
2. 实现 `RUNCODE.md` / `CLAUDE.md` loader，让 prompt 的 project context 真正落地。
3. 把 permission mode 和常见权限拒绝原因注入 prompt 或 tool result，帮助模型自我修正。
4. 增加 `/clear` 或最小 history reset 入口。
5. 再考虑 TodoWrite、streaming output、持久 transcript、TUI、MCP 等更大功能。
