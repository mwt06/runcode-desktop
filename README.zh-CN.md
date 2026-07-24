# runcode（奔跑的代码）

> 一个用 Go 实现的开源 AI 编程伴侣。
> English: see [README.md](./README.md)。

[![CI](https://github.com/wt68/runcode/actions/workflows/ci.yml/badge.svg)](https://github.com/wt68/runcode/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wt68/runcode.svg)](https://pkg.go.dev/github.com/wt68/runcode)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)

> **状态：alpha。** 本仓库是同一引擎的**外壳**集合：shell 友好的 `chat` CLI、Bubble Tea `tui`、Wails 桌面应用（**XRUN**，见 [docs/desktop.md](./docs/desktop.md)），以及服务端骨架（`cmd/runcode-server`）。引擎本体——ReAct 循环、14 个内置工具、MCP / skills / 子代理 / 记忆 / hooks、带模型判害门的权限模式、会话持久化与上下文压缩——在独立仓库：**`gitlab.ouc-online.com.cn/aibase/agentloop`**。

## runcode 是什么？

`runcode` 是一个 Go 版 AI 编程伴侣。本仓库只含面向用户的前端；传输无关的会话引擎是外部 `agentloop` module，经各 `go.mod` 的 `replace` 指向同级 checkout。长期方向参考 Anthropic Claude Code 的核心思想，但全部代码为原创 Go 实现。

- **CLI**（`runcode chat`）：流式输出到 stdout，shell 友好。
- **TUI**（`runcode tui`）：Bubble Tea 界面——流式 Markdown、工具卡片、权限弹窗、slash 命令。
- **桌面**（**XRUN**，`cmd/runcode-desktop`）：完整的 Wails + React 应用，带 judge/flight 权限模式、子代理卡片、产物预览。
- **服务端骨架**（`cmd/runcode-server`）：只依赖引擎公开面的可跑 HTTP/SSE 参考实现——独立服务端仓库的模板。

## 快速开始

日常开发把引擎仓库 checkout 到本仓库**同级**——已提交的 `go.work` 会把两者联动，改引擎实时生效：

```bash
git clone https://github.com/wt68/runcode.git
git clone https://gitlab.ouc-online.com.cn/aibase/agentloop.git agentloop   # 同级 checkout(go.work 联动)
cd runcode
go build ./cmd/runcode
./runcode version
./runcode --help
```

不想要同级 checkout(CI 式构建)时，直接拉取 tag 版本的引擎：

```bash
GOWORK=off GOPRIVATE=gitlab.ouc-online.com.cn go build ./cmd/runcode
```

> 需要 Go 1.26+;直连拉取需有内网 GitLab 的 git 凭证。

## 当前 CLI

```bash
ANTHROPIC_MODEL=claude-sonnet-5 \
ANTHROPIC_API_KEY=... \
./runcode chat "总结这个仓库"

ANTHROPIC_MODEL=claude-sonnet-5 \
ANTHROPIC_API_KEY=... \
./runcode tui
```

七个子命令：`version`、`chat`（`--loop` 多轮）、`tui`（`--pick` 打开会话选择器）、`config`（打印生效配置与来源，凭证脱敏）、`permissions`、`sessions`（`list`/`show`）、`transcript`（`list`/`search`）。

主要 flag 与环境变量（`chat`/`tui` 共用）：

- `--provider` / `RUNCODE_PROVIDER`：`anthropic` 或 `openai`（后者兼容 vLLM/Ollama/llama.cpp/网关等 OpenAI 风格端点；`--base-url` 指向提供 `/chat/completions` 的 API 根）。
- `--model` / `ANTHROPIC_MODEL`；`--api-key` / `ANTHROPIC_API_KEY` 或 `--auth-token` / `ANTHROPIC_AUTH_TOKEN`；`--base-url` / `ANTHROPIC_BASE_URL`。
- `--cwd` / `RUNCODE_CWD`：工具的工作区。
- `--permission-mode safe|interactive` / `RUNCODE_PERMISSION_MODE`（桌面版另有 `judge`/`flight`）。
- `--thinking off|low|medium|high` / `RUNCODE_THINKING`。
- `--resume <id>` / `--continue` / `--no-session`；`--session-backend jsonl|sqlite` / `RUNCODE_SESSION_BACKEND`。
- `--transcript off|jsonl|sqlite` / `RUNCODE_TRANSCRIPT`；`--telemetry off|jsonl` / `RUNCODE_TELEMETRY`。
- `--max-history-messages`、`--max-context-tokens`（压缩预算）、`--max-retries`、`--input-price` / `--output-price`（供 `/cost`）、`--allow-mcp-sampling`。
- `--system-prompt` / `--append-system-prompt`。

TUI slash 命令：`/help`、`/clear`、`/compact`、`/status`、`/mode`、`/model`、`/cost`、`/exit`，另合并用户/项目 `commands/` 目录发现的自定义 `*.md` 命令。

## 功能（引擎）

以下行为由 [agentloop](https://gitlab.ouc-online.com.cn/aibase/agentloop) 引擎提供、经每个外壳呈现；细节见彼侧 README 与 docs。

- **工具**：14 个内置（Read/Write/Edit/Delete/Glob/Grep/Bash+后台 shell/TodoWrite/WebFetch/WebSearch/Analyze/AskUser），另动态追加 MCP 工具、`Skill`、`Task`（子代理）与 `Remember`（记忆）。
- **权限**：每次工具调用先分类再授权；CLI/TUI 提供 `safe`（非交互拒绝）与 `interactive`（逐项询问），桌面版另有 `judge`（模型判害门自动放行，带确定性下限、预算熔断与审计事件）与 `flight`。授权持久化在 `<workspace>/.runcode/permissions.json`，由 `runcode permissions` 管理。
- **MCP**：stdio 与 Streamable HTTP 两种传输，仅在**用户级** `config.toml` 配置（`[mcp.servers.<name>]`，支持 `${VAR}` 展开）；工具以 `mcp__<server>__<tool>` 呈现；支持 resources/prompts/roots，sampling 显式开启。
- **Skills / 子代理 / 记忆 / hooks**：`SKILL.md` 目录约定 + 渐进式披露；`*.md` 代理定义经 `Task` 委托（恰一层、可并发 fan-out）；`Remember` 两 scope 持久记忆；8 个生命周期 hook 事件（仅用户级配置，argv 直执、JSON 走 stdin）。
- **会话**：按工作区持久化全量历史（`jsonl` 或纯 Go `sqlite` 后端），resume/continue、自动标题、token 预算上下文压缩；另有脱敏可检索 transcript（FTS5）作为独立的可选记录。
- **Provider**：Anthropic（官方 SDK）与 OpenAI 兼容 HTTP/SSE,支持 thinking 预算、图片输入、提示词缓存边界、连接期重试。
- **配置**：TOML 文件,优先级 flag > env > 项目 `runcode.toml` > 用户 `config.toml` > 默认；凭证、MCP servers、hooks **只认**用户级文件。

## 架构一瞥

```text
用户输入
  -> cmd/runcode chat | tui            (本仓库)
  -> XRUN 桌面 / 服务端骨架             (本仓库)
       -> engine.Build(cfg, Options{...}) -> Session   (agentloop,外部 module)
            系统提示 + tool specs -> 模型流 -> tool_use
            -> executor -> 权限层 -> Tool.Run -> tool_result
       -> 流式文本 / 工具事件 / 审批请求回到外壳
```

- [docs/architecture.md](./docs/architecture.md) —— 本仓库外壳、模块边界、协议代码生成。
- [docs/desktop.md](./docs/desktop.md) —— 桌面应用(XRUN)架构与构建。
- agentloop 的 `README.md` 与 `docs/` —— 引擎内部(ReAct 循环、权限、工具、provider、持久化)。

## 仓库布局

```text
cmd/runcode/             Cobra CLI:version、chat、tui、config、permissions、sessions、transcript
cmd/runcode-desktop/     嵌套 Go module:Wails 桌面外壳 + React 前端(XRUN)
cmd/runcode-server/      嵌套 Go module:服务端骨架(HTTP/SSE,只依赖引擎公开面)
internal/desktop/        桌面核心(host.Manager 适配层、事件、审批器;不依赖 Wails)
internal/ui/             Bubble Tea TUI:视图、slash 命令注册表、会话选择器、审批桥
internal/command/        自定义 slash 命令(*.md 发现)
internal/previewtool/    桌面产物预览工具(经 ExtraTools 注入)
tools/protogen/          协议 TS 代码生成器(读 agentloop/protocol,写前端 src/protocol/)
```

## 参与贡献

项目处于 **alpha**。引擎侧贡献(工具、provider、权限模型)去 agentloop 仓库;本仓库接收外壳侧工作(CLI/TUI/桌面/服务端)。见 [CONTRIBUTING.md](./CONTRIBUTING.md)。

## 许可证

Apache-2.0 —— 见 [LICENSE](./LICENSE)。

## 致谢

架构理念受 Anthropic Claude Code CLI 启发。本仓库所有 Go 代码均为原创实现。
