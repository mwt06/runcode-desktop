# runcode（奔跑的代码）

> 一个用 Go 实现的开源终端 AI 编程伴侣。
> English: see [README.md](./README.md)。

[![CI](https://github.com/wt68/runcode/actions/workflows/ci.yml/badge.svg)](https://github.com/wt68/runcode/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wt68/runcode.svg)](https://pkg.go.dev/github.com/wt68/runcode)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)

> **状态：v0.1-alpha。** 当前已经有最小 provider-backed `chat` 命令、内存态 ReAct loop、最小 Bubble Tea `tui` 命令、CLI chat 的 safe/interactive 权限、telemetry，以及内置 `Read`/`Write`/`Edit`/`Glob`/`Grep`/`Bash` 工具；但还不是完整 TUI 产品。

## runcode 是什么？

`runcode` 是一个面向终端的 Go 版 AI 编程伴侣。当前版本刻意保持小范围：通过 `runcode chat` 调用 Anthropic provider，暴露一组受限本地工具，并在文件修改和命令执行前经过内部权限层。

长期方向参考 Anthropic Claude Code 的核心思想，但本仓库是原创 Go 实现。Bubble Tea TUI 当前是最小 MVP；MCP、hooks、sub-agents、skills、SQLite transcript、上下文压缩、更完整的 TUI 权限/工具界面、多 provider 等系统目前仍是脚手架或后续工作。

## 快速开始

```bash
git clone https://github.com/wt68/runcode.git
cd runcode
go build ./cmd/runcode
./runcode version
./runcode --help
```

> 需要 Go 1.26+。

## 当前 CLI

```bash
ANTHROPIC_MODEL=claude-sonnet-4-6 \
ANTHROPIC_API_KEY=... \
./runcode chat "summarize this repository"

ANTHROPIC_MODEL=claude-sonnet-4-6 \
ANTHROPIC_API_KEY=... \
./runcode tui
```

`runcode chat` 会把 assistant text delta 实时写到 stdout。`runcode tui` 会启动一个最小 Bubble Tea 界面，包含 Claude Code 风格底部状态区、累计上下文 token 与思考模式指示、可滚动对话 viewport、上下分隔线包裹的单行输入、assistant 流式 Markdown 渲染、带安全文件摘要的树状工具进度卡片，以及 `/help`、`/clear`、`/status`、`/exit`。使用 `--permission-mode interactive` 时，TUI 会弹出权限审批弹窗，提供「允许一次 / 本会话允许 / 拒绝」三个选项；选择「本会话允许」后，本会话内等价操作不再重复询问。工具卡片会展示脱敏的输出摘要（Bash stdout/stderr、Grep 匹配行、Read 预览）以及 Edit/Write 的完整行级 diff，可用 `ctrl+o` 展开。

常用参数和环境变量：

- `--provider` / `RUNCODE_PROVIDER`：当前只支持 `anthropic`。
- `--model` / `ANTHROPIC_MODEL`：必须通过 flag 或环境变量提供。
- `--api-key` / `ANTHROPIC_API_KEY`，或 `--auth-token` / `ANTHROPIC_AUTH_TOKEN`。
- `--base-url` / `ANTHROPIC_BASE_URL`。
- `--cwd` / `RUNCODE_CWD`：工具工作目录。
- `--loop`：在 stdin 多轮输入中复用同一个内存 session；可用 `/clear` 清空该内存 history。
- `--max-history-messages` / `RUNCODE_MAX_HISTORY_MESSAGES`：限制每轮发送给 provider 的内存 history 消息数（`0` 表示不限制，为默认值）。裁剪会完整保留当前 turn，绝不拆散 `tool_use`/`tool_result` 配对，也不影响 transcript 文件。
- `--permission-mode safe|interactive` / `RUNCODE_PERMISSION_MODE`。
- `--telemetry off|jsonl` / `RUNCODE_TELEMETRY`。
- `--transcript off|jsonl` / `RUNCODE_TRANSCRIPT`：可选把 JSONL transcript 写入 `<workspace>/.runcode/transcripts/`。
- `--session-id` / `RUNCODE_SESSION_ID`：开启 transcript 时指定 transcript 文件名。

当前限制：

- TUI 仍是 MVP：还没有权限审批弹窗、diff viewer、文件树、transcript 浏览器或多行输入。
- 没有 transcript-backed session 恢复；JSONL transcript 是 append-only 且默认关闭。
- 没有完整 slash commands 系统、MCP、hooks、sub-agents、skills 或 OpenAI provider。

## 已实现工具

内置工具由 `tools.Builtins()` 注册，同时暴露给模型 tool spec 和 prompt 工具摘要：

| 工具 | 当前效果 |
|------|----------|
| `Read` | 读取 workspace 文件，返回行号文本，并记录完整/部分读取 metadata。 |
| `Write` | 在 workspace 内创建文件，或覆盖已 fresh-read 的文件。 |
| `Edit` | 在 workspace 内对已 fresh-read 文件做 exact string replacement。 |
| `Glob` | 用 slash glob pattern 和 `**` 查找 workspace 文件；可与兄弟 safe 工具调用并发执行。 |
| `Grep` | 用 Go regexp 搜索 workspace 文本文件；可与兄弟 safe 工具调用并发执行。 |
| `Bash` | 权限审批后，在 workspace 内执行单行非交互 Bash 命令。 |

`TodoWrite`、WebFetch/WebSearch、MCP tools 和插件工具尚未实现。

## 权限与安全

Executor 在运行每个工具前都会调用 `internal/permissions`：

- workspace 内 `Read`/`Glob`/`Grep` 默认允许。
- `Write`/`Edit` 需要审批，并且覆盖/编辑前要求 fresh-read。
- `Bash` 执行前会分类命令；unknown、privileged、destructive、outside-write、complex shell-control 命令在审批前直接拒绝。
- `safe` 模式是非交互模式，所有需要审批的动作最终都会拒绝。
- `interactive` 模式只对权限层已判定为可审批的动作在 stderr 询问一次。

Telemetry 只记录 operation、risk、resource scope、permission effect、command classification 等受控 metadata；不记录 raw path、raw command、tool input、tool output、文件内容、凭证或 URL。

Transcript 默认关闭。使用 `--transcript jsonl` 开启后，runcode 会把 append-only turn record 写到 `<workspace>/.runcode/transcripts/<session-id>.jsonl`；记录包含用户文本、最终 assistant 文本、受限工具摘要和 Bash command 字符串，但不记录 system prompt、凭证、普通工具 raw input 或完整工具输出。

## 架构一览

```text
用户输入
  -> cmd/runcode chat OR cmd/runcode tui
  -> shared chat config/session factory
  -> Anthropic provider
  -> internal/repl.Session
  -> prompt.BuildSystemPrompt + tools.Builtins tool specs
  -> model stream
  -> tool_use
  -> internal/repl.Executor
  -> internal/permissions.Service
  -> Tool.Run
  -> tool_result
  -> chat stdout OR TUI StreamDelta event
```

更多说明：

- [docs/architecture.md](./docs/architecture.md)：当前已实现架构。
- [docs/data-flow-and-prompt.md](./docs/data-flow-and-prompt.md)：请求、工具、prompt 数据流。
- [docs/implementation-status.md](./docs/implementation-status.md)：当前缺口和最小化实现边界。

## 项目布局

```text
cmd/runcode/           Cobra CLI：version、chat 和最小 tui
internal/ui/           Bubble Tea TUI MVP：底部状态区、viewport、输入框、Markdown 渲染、工具进度/文件摘要、slash commands
internal/repl/         ReAct session、executor、tool result conversion、telemetry
internal/permissions/  action/resource/risk、policy、approval、command classification
internal/prompt/       系统提示组装器和 cache boundary
internal/telemetry/    event model、JSONL、async、memory recorder
internal/persistence/  可选 JSONL transcript 记录
internal/toolpath/     workspace path 解析和 fresh-read gate
pkg/tool/              public tool interface、schema、context、result types
pkg/llm/               provider-neutral LLM DTO 和 stream interface
tools/                 内置工具和 registry
docs/                  当前架构、数据流、handoff、状态说明
```

仍是脚手架或未实现：`internal/mcp`、`internal/hooks`、SQLite/session resume persistence、`internal/compaction`、`internal/cost`、`pkg/agent`、`pkg/skill`、`pkg/command`、`pkg/plugin`、`pkg/llm/providers/openai`、`tools/todo`、`prompts/*`。

## 贡献

项目处于 **alpha** 阶段。`pkg/` SDK **在 v1.0 前不稳定**。详见 [CONTRIBUTING.md](./CONTRIBUTING.md)。

## 许可证

Apache-2.0 — 见 [LICENSE](./LICENSE)。

## 致谢

架构概念参考自 Anthropic Claude Code CLI。此仓库中的 Go 代码均为原创实现。
