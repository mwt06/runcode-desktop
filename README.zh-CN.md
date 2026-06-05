# runcode（奔跑的代码）

> 一个用 Go 实现的开源终端 AI 编程伴侣。
> English: see [README.md](./README.md)。

[![CI](https://github.com/wt68/runcode/actions/workflows/ci.yml/badge.svg)](https://github.com/wt68/runcode/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wt68/runcode.svg)](https://pkg.go.dev/github.com/wt68/runcode)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)

> **状态：v0.1-alpha。** 当前已经有最小 provider-backed `chat` 命令、内存态 ReAct loop、最小 Bubble Tea `tui` 命令、CLI chat 的 safe/interactive 权限、telemetry，以及内置 `Read`/`Write`/`Edit`/`Glob`/`Grep`/`Bash` 工具；但还不是完整 TUI 产品。

## runcode 是什么？

`runcode` 是一个面向终端的 Go 版 AI 编程伴侣。当前版本刻意保持小范围：通过 `runcode chat` 调用 Anthropic provider，暴露一组受限本地工具，并在文件修改和命令执行前经过内部权限层。

长期方向参考 Anthropic Claude Code 的核心思想，但本仓库是原创 Go 实现。Bubble Tea TUI 当前是最小 MVP；hooks、sub-agents、SQLite transcript、更完整的 TUI 权限/工具界面、更广的多 provider 等系统目前仍是脚手架或后续工作。

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

`runcode chat` 会把 assistant text delta 实时写到 stdout。`runcode tui` 会启动一个最小 Bubble Tea 界面，包含 Claude Code 风格底部状态区、累计上下文 token 与思考模式指示、可滚动对话 viewport、上下分隔线包裹的多行输入（Enter 发送；`alt+enter`/`ctrl+j` 换行；↑/↓ 翻阅已提交输入的历史，或在多行草稿内移动光标）、assistant 流式 Markdown 渲染、带安全文件摘要的树状工具进度卡片，以及 slash 命令（`/help`、`/clear`、`/status`、`/mode`、`/model`、`/compact`、`/cost`、`/exit`），输入 `/` 会弹出可过滤的命令菜单。使用 `--permission-mode interactive` 时，TUI 会弹出权限审批弹窗，提供「允许一次 / 本会话允许 / 拒绝」三个选项；选择「本会话允许」后，本会话内等价操作不再重复询问。工具卡片会展示脱敏的输出摘要（Bash stdout/stderr、Grep 匹配行、Read 预览）以及 Edit/Write 的完整行级 diff，可用 `ctrl+o` 展开。

常用参数和环境变量：

- `--provider` / `RUNCODE_PROVIDER`：`anthropic` 或 `openai`（后者同时支持各类 OpenAI 兼容端点，如 vLLM/Ollama/llama.cpp/网关；`--base-url` 指向提供 `/chat/completions` 的 API 根路径，本地无鉴权端点可不填凭证）。
- `--model` / `ANTHROPIC_MODEL`：必须通过 flag 或环境变量提供。
- `--api-key` / `ANTHROPIC_API_KEY`，或 `--auth-token` / `ANTHROPIC_AUTH_TOKEN`。
- `--base-url` / `ANTHROPIC_BASE_URL`。
- `--max-retries` / `RUNCODE_MAX_RETRIES`：provider 瞬时失败重试次数（0 = 默认，负数 = 禁用）。
- `--input-price` / `--output-price`（`RUNCODE_INPUT_PRICE` / `RUNCODE_OUTPUT_PRICE`）：每百万 token 单价，用于 TUI `/cost` 估算。未设置时按 model 名从内置定价表查找（Claude 4.x 家族与常见 OpenAI 模型）；显式单价始终优先，未知 model（如自托管端点）保持不计价。内置单价为近似值——需精确账单请显式设置。
- `--cwd` / `RUNCODE_CWD`：工具工作目录。
- `--loop`：在 stdin 多轮输入中复用同一个内存 session；可用 `/clear` 清空该内存 history。
- `--max-history-messages` / `RUNCODE_MAX_HISTORY_MESSAGES`：限制每轮发送给 provider 的内存 history 消息数（`0` 表示不限制，为默认值）。裁剪会完整保留当前 turn，绝不拆散 `tool_use`/`tool_result` 配对，也不影响 transcript 文件。
- `--permission-mode safe|interactive` / `RUNCODE_PERMISSION_MODE`。
- `--telemetry off|jsonl` / `RUNCODE_TELEMETRY`。
- `--transcript off|jsonl` / `RUNCODE_TRANSCRIPT`：可选把 JSONL transcript 写入 `<workspace>/.runcode/transcripts/`。
- `--session-id` / `RUNCODE_SESSION_ID`：开启 transcript 时指定 transcript 文件名。

### 配置文件

runcode 还支持 TOML 配置文件，优先级为 **flag > 环境变量 > 项目配置 > 用户配置 > 默认**：

- 项目级：`runcode.toml`，从工作目录向上逐级查找。
- 用户级：`config.toml`，位于 `os.UserConfigDir()/runcode/`（Windows=`%AppData%\runcode\config.toml`，Linux=`~/.config/runcode/config.toml`，macOS=`~/Library/Application Support/runcode/config.toml`）。

支持的字段：`provider`、`model`、`base_url`、`max_tokens`、`permission_mode`、`telemetry`、`transcript`、`max_history_messages`，以及 **仅用户级文件生效** 的 `api_key` / `auth_token` 和 `[mcp.servers.*]`（见下）。项目级文件中的凭证会被忽略，避免误提交。

### MCP 服务器（Model Context Protocol）

runcode 可以连接 MCP 服务器并把它们的工具暴露给模型。服务器在 **用户级** `config.toml` 的 `[mcp.servers.<name>]` 下配置——**仅用户级生效**，项目文件绝不能仅凭存在就让 runcode 启动子进程或访问端点。每个服务器通过 **stdio**（本地子进程）或 **Streamable HTTP**（远程端点）连接。字符串值支持 `${VAR}` 展开，让密钥留在环境变量而不是文件里。

```toml
# stdio：作为子进程启动的本地服务器（默认传输）
[mcp.servers.filesystem]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/expose"]
env = { SOME_TOKEN = "${SOME_TOKEN}" }   # ${VAR} 从环境变量展开
# enabled = false                         # 省略或设 true 即启用

# http：通过 Streamable HTTP 的远程服务器
[mcp.servers.docs]
transport = "http"
url = "https://example.com/mcp"
headers = { Authorization = "Bearer ${DOCS_TOKEN}" }
```

服务器的工具以 `mcp__<server>__<tool>` 暴露给模型。它们被归类为 **external** 权限操作：每次调用都需审批（safe 模式拒绝），「本会话/项目允许」按 server+tool 记忆。连接失败的服务器会作为 warning 报告并跳过，绝不中断会话。服务器名只能用字母、数字、`-`、`_`，且不含 `__`。

若连接的服务器声明了 **resources** 能力,会额外暴露两个内置工具:`ListMcpResources`(列出各 server 的资源——uri、name、description,可按 server 过滤)与 `ReadMcpResource`(按 server + uri 读取资源内容)。文本直通,二进制内容以占位提示。与 server 工具调用一样,两者都是 external 操作需审批;读取按 server+uri 记忆 grant。资源工具仅在至少一个 server 支持 resources 时出现。MCP prompts、sampling、roots、资源模板与订阅暂未实现。

### Skills（技能）

**skill** 是一个可复用工作流：一个目录,内含 `SKILL.md`,其 frontmatter 声明 `name` 与 `description`,正文是详细指令。skills 从两个约定目录发现——`<userConfigDir>/runcode/skills/`（用户级）与 `<workspace>/.runcode/skills/`（项目级）——同名时用户级 skill 覆盖项目级。无需任何配置,放一个目录进去即可。

```text
~/.config/runcode/skills/code-review/SKILL.md   # Windows 上为 %AppData%\runcode\skills\...
```

```markdown
---
name: code-review
description: Review the current diff for correctness and clarity
---

1. 用 `git diff` 读取改动。
2. 逐项检查 ...
```

为节省上下文,只把一段紧凑的 **catalog**（每个 skill 的名字与描述,项目级标注 `[project]`）注入 system prompt。模型在需要时调用内置 **`Skill`** 工具并传入名字来按需加载该 skill 的完整指令——未用到的 skill 不占上下文。`Skill` 工具只返回内存中的文本（不启动任何进程、不碰文件）,因此免审批运行。格式错误或超大的 skill 会被跳过并告警,而非中断会话。项目级 skill 生效(便于团队在仓库内共享工作流),但其文本会进入 prompt,故标注 `[project]`。`runcode config` 会列出已加载的 skill 名字（项目级带标注）,不打印任何正文。

运行 `runcode config` 可查看生效配置和已加载的配置文件路径（凭证值绝不打印）。

```toml
# runcode.toml
model = "claude-opus-4-8"
base_url = "https://api.anthropic.com"
permission_mode = "interactive"
```

### 会话恢复与上下文压缩

runcode 默认把每个会话的完整对话保存到 `<workspace>/.runcode/sessions/<id>.jsonl`，可跨进程接着上次继续：

- `--resume <id>`：恢复指定会话并继续。
- `--continue`：恢复本 workspace 最近一次会话。
- `--no-session` / `RUNCODE_SESSION_PERSIST=off`：关闭历史持久化。

设置 `--max-context-tokens`（或配置文件 `max_context_tokens`）可限制上下文：当某轮的 input tokens 接近预算时，runcode 会把最旧的若干 turn 总结成一条消息，保留最近 turn 原文。压缩只作用于内存工作集——磁盘历史保持完整。`/clear` 只清内存上下文，磁盘会话日志仍是完整的 append-only 记录。

会话日志是无损的，可能包含文件内容与命令输出；它以 `0600` 写在 workspace 内，并被 `.gitignore` 忽略。

当前限制：

- TUI 仍是 MVP：已有权限审批弹窗、rich tool output（输出摘要 + Edit/Write 行级 diff），以及可随内容增高的多行输入和已提交输入的历史翻阅，但还没有文件树、transcript 浏览器或语法高亮。
- 没有 transcript-backed session 恢复；JSONL transcript 是 append-only 且默认关闭。
- slash 命令已是可扩展注册表（内置 `/help`、`/clear`、`/status`、`/mode`、`/model`、`/compact`、`/cost`、`/exit`；`/mode safe|interactive` 运行时切权限模式、`/model <name>` 运行时切模型）。已支持 MCP 工具（stdio + Streamable HTTP，tools 原语，见 [MCP 服务器](#mcp-服务器model-context-protocol)）与 skills（经 `Skill` 工具渐进式披露，见 [Skills](#skills技能)）和 MCP resources（`ListMcpResources`/`ReadMcpResource`）；尚无 hooks、sub-agents,也暂无 MCP prompts/sampling/roots。

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
| `TodoWrite` | 记录当前任务清单（每项含 content/status/activeForm）；无副作用，免审批。 |
| `WebFetch` | 抓取 http(s) URL 并返回文本（HTML 转纯文本）；网络操作，需审批（按 host 显示）。 |

配置后,MCP 服务器工具也会以 `mcp__<server>__<tool>` 动态暴露（见 [MCP 服务器](#mcp-服务器model-context-protocol)）,`Skill` 工具也会按需加载可复用工作流（见 [Skills](#skills技能)）。WebSearch 和插件工具尚未实现。

## 权限与安全

Executor 在运行每个工具前都会调用 `internal/permissions`：

- workspace 内 `Read`/`Glob`/`Grep` 默认允许。
- `Write`/`Edit` 需要审批，并且覆盖/编辑前要求 fresh-read。
- `Bash` 执行前会分类命令；unknown、privileged、destructive、outside-write、complex shell-control 命令在审批前直接拒绝。
- `WebFetch`（network）与 MCP 服务器工具（external）始终需要审批；「本会话/项目允许」分别按 host、按 server+tool 记忆。
- `safe` 模式是非交互模式，所有需要审批的动作最终都会拒绝。
- `interactive` 模式只对权限层已判定为可审批的动作在 stderr 询问一次。审批提供 allow once / allow for session / allow for project；选「allow for project」会持久化到 `<workspace>/.runcode/permissions.json`（0600，已 gitignore），跨进程生效。该文件还承载一个 denylist，在询问前检查（deny 始终优先于 allow）；文件损坏时快速报错，而非静默丢弃 deny 规则。

`runcode permissions` 用来管理该文件，无需手改 JSON：`permissions list` 给持久化的 allow/deny 规则编号，`permissions remove <n>` 按编号删除任意一条（包括 TUI 写入的 mutation/command 规则），`permissions clear [--allow|--deny]` 清空。`permissions deny <host>` / `permissions allow <host>` 按 host 增删网络规则（默认工具 `WebFetch`）——这是唯一可手输且能精确匹配的规则类型；deny 始终优先,因此 host 在 denylist 上时 allow 会被拒绝,需先删除该 deny。

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
internal/mcp/          Model Context Protocol 客户端：JSON-RPC、stdio + HTTP 传输、工具适配、manager
internal/prompt/       系统提示组装器和 cache boundary
internal/telemetry/    event model、JSONL、async、memory recorder
internal/persistence/  可选 JSONL transcript 记录
internal/toolpath/     workspace path 解析和 fresh-read gate
pkg/tool/              public tool interface、schema、context、result types
pkg/llm/               provider-neutral LLM DTO 和 stream interface
tools/                 内置工具和 registry
docs/                  当前架构、数据流、handoff、状态说明
```

仍是脚手架或未实现：`internal/hooks`、SQLite transcript persistence、`pkg/agent`、`pkg/command`、`pkg/plugin`、`prompts/agents` / `prompts/templates`。

## 贡献

项目处于 **alpha** 阶段。`pkg/` SDK **在 v1.0 前不稳定**。详见 [CONTRIBUTING.md](./CONTRIBUTING.md)。

## 许可证

Apache-2.0 — 见 [LICENSE](./LICENSE)。

## 致谢

架构概念参考自 Anthropic Claude Code CLI。此仓库中的 Go 代码均为原创实现。
