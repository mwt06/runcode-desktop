# runcode

> An open-source AI coding companion in Go.
> 中文名：**奔跑的代码** — see [README.zh-CN.md](./README.zh-CN.md).

[![CI](https://github.com/wt68/runcode/actions/workflows/ci.yml/badge.svg)](https://github.com/wt68/runcode/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wt68/runcode.svg)](https://pkg.go.dev/github.com/wt68/runcode)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)

> **Status: alpha.** This repository holds the **shells** over one shared engine: a shell-friendly `chat` CLI, a Bubble Tea `tui`, a Wails desktop app (**XRUN**, see [docs/desktop.md](./docs/desktop.md)), and a server skeleton (`cmd/runcode-server`). The engine itself — ReAct loop, 14 built-in tools, MCP / skills / sub-agents / memory / hooks, permission modes with a model-based harm gate, session persistence and compaction — lives in its own repository: **`gitlab.ouc-online.com.cn/aibase/agentloop`**.

## What is runcode?

`runcode` is a Go implementation of an AI coding companion. This repository contains the user-facing frontends; the transport-agnostic session engine is the external `agentloop` module, consumed through each `go.mod`'s `replace` pointing at a sibling checkout. The long-term direction is inspired by Anthropic's Claude Code, but all code is an original Go implementation.

- **CLI** (`runcode chat`): streams assistant output to stdout, shell-friendly.
- **TUI** (`runcode tui`): Bubble Tea interface with streaming Markdown, tool cards, permission modals, slash commands.
- **Desktop** (**XRUN**, `cmd/runcode-desktop`): full Wails + React app with judge/flight permission modes, sub-agent cards, artifact preview.
- **Server skeleton** (`cmd/runcode-server`): a runnable HTTP/SSE reference that consumes only the engine's public surface — the template for a standalone server repo.

## Quick start

For development, check out the engine repo **next to** this one — the committed `go.work` links them so engine edits take effect live:

```bash
git clone https://github.com/wt68/runcode.git
git clone https://gitlab.ouc-online.com.cn/aibase/agentloop.git agentloop   # sibling checkout (go.work links it)
cd runcode
go build ./cmd/runcode
./runcode version
./runcode --help
```

To build without the sibling checkout (CI-style), fetch the tagged engine directly instead:

```bash
GOWORK=off GOPRIVATE=gitlab.ouc-online.com.cn go build ./cmd/runcode
```

> Requires Go 1.26+ and, for the direct fetch, git credentials for the internal GitLab.

## Current CLI

```bash
ANTHROPIC_MODEL=claude-sonnet-5 \
ANTHROPIC_API_KEY=... \
./runcode chat "summarize this repository"

ANTHROPIC_MODEL=claude-sonnet-5 \
ANTHROPIC_API_KEY=... \
./runcode tui
```

Seven subcommands: `version`, `chat` (`--loop` for multi-turn), `tui` (`--pick` opens a session picker), `config` (effective config with sources, credentials redacted), `permissions`, `sessions` (`list`/`show`), `transcript` (`list`/`search`).

Useful flags and environment variables (shared by `chat`/`tui`):

- `--provider` / `RUNCODE_PROVIDER`: `anthropic` or `openai` (the latter also drives OpenAI-compatible endpoints such as vLLM/Ollama/llama.cpp/gateways; point `--base-url` at the API root serving `/chat/completions`).
- `--model` / `ANTHROPIC_MODEL`; `--api-key` / `ANTHROPIC_API_KEY` or `--auth-token` / `ANTHROPIC_AUTH_TOKEN`; `--base-url` / `ANTHROPIC_BASE_URL`.
- `--cwd` / `RUNCODE_CWD`: workspace for tools.
- `--permission-mode safe|interactive` / `RUNCODE_PERMISSION_MODE` (the desktop additionally offers `judge`/`flight`).
- `--thinking off|low|medium|high` / `RUNCODE_THINKING`.
- `--resume <id>` / `--continue` / `--no-session`; `--session-backend jsonl|sqlite` / `RUNCODE_SESSION_BACKEND`.
- `--transcript off|jsonl|sqlite` / `RUNCODE_TRANSCRIPT`; `--telemetry off|jsonl` / `RUNCODE_TELEMETRY`.
- `--max-history-messages`, `--max-context-tokens` (context compaction budget), `--max-retries`, `--input-price` / `--output-price` (for `/cost`), `--allow-mcp-sampling`.
- `--system-prompt` / `--append-system-prompt`.

TUI slash commands: `/help`, `/clear`, `/compact`, `/status`, `/mode`, `/model`, `/cost`, `/exit`, plus custom `*.md` commands discovered from user/project `commands/` directories.

## Features (engine)

These behaviors ship in the [agentloop](https://gitlab.ouc-online.com.cn/aibase/agentloop) engine and surface through every shell; details live in that repo's README and docs.

- **Tools**: 14 built-ins (Read/Write/Edit/Delete/Glob/Grep/Bash + background shells/TodoWrite/WebFetch/WebSearch/Analyze/AskUser), plus dynamically added MCP tools, `Skill`, `Task` (sub-agents), and `Remember` (memory).
- **Permissions**: every tool call is classified and authorized; `safe` (non-interactive deny) and `interactive` (ask) in the CLI/TUI, plus `judge` (model harm-gate auto-allow with deterministic floors, budget breaker, and audit events) and `flight` in the desktop. Grants persist to `<workspace>/.runcode/permissions.json`, managed by `runcode permissions`.
- **MCP**: stdio + Streamable HTTP servers configured in the **user-level** `config.toml` only (`[mcp.servers.<name>]`, `${VAR}` expansion); tools appear as `mcp__<server>__<tool>`; resources/prompts/roots supported, sampling opt-in.
- **Skills / Sub-agents / Memory / Hooks**: `SKILL.md` directories with progressive disclosure; `*.md` agent definitions delegated via `Task` (one level deep, concurrent fan-out); two-scope persistent memory via `Remember`; 8 lifecycle hook events (user-level config only, argv exec, JSON on stdin).
- **Sessions**: full history persisted per workspace (`jsonl` or pure-Go `sqlite` backend), resume/continue, generated titles, token-budget context compaction; sanitized searchable transcripts (FTS5) as a separate opt-in record.
- **Providers**: Anthropic (official SDK) and OpenAI-compatible HTTP/SSE, with thinking budgets, image input, prompt-cache boundaries, and connection-phase retries.
- **Configuration**: TOML files with precedence flag > env > project `runcode.toml` > user `config.toml` > default; credentials, MCP servers, and hooks are honored **only** from the user-level file.

## Architecture at a glance

```text
user input
  -> cmd/runcode chat | tui        (this repo)
  -> XRUN desktop / server skeleton (this repo)
       -> engine.Build(cfg, Options{...}) -> Session   (agentloop, external module)
            system prompt + tool specs -> model stream -> tool_use
            -> executor -> permissions -> Tool.Run -> tool_result
       -> streamed text / tool events / approval requests back to the shell
```

- [docs/architecture.md](./docs/architecture.md) — this repo's shells, module boundaries, protocol codegen.
- [docs/desktop.md](./docs/desktop.md) — the desktop app (XRUN) architecture and build.
- agentloop's `README.md` and `docs/` — engine internals (ReAct loop, permissions, tools, providers, persistence).

## Project layout

```text
cmd/runcode/             Cobra CLI: version, chat, tui, config, permissions, sessions, transcript
cmd/runcode-desktop/     nested Go module: Wails desktop shell + React frontend (XRUN)
cmd/runcode-server/      nested Go module: server skeleton (HTTP/SSE, engine public surface only)
internal/desktop/        desktop core (host.Manager adapter, events, approver; no Wails dependency)
internal/ui/             Bubble Tea TUI: views, slash-command registry, session picker, approval bridge
internal/command/        custom slash commands (*.md discovery)
internal/previewtool/    desktop artifact-preview tool (injected via ExtraTools)
tools/protogen/          protocol TS code generator (reads agentloop/protocol, writes frontend src/protocol/)
```

## Contributing

This project is in **alpha**. Engine contributions (tools, providers, permission model) go to the agentloop repo; this repo takes shell work (CLI/TUI/desktop/server). See [CONTRIBUTING.md](./CONTRIBUTING.md).

## License

Apache-2.0 — see [LICENSE](./LICENSE).

## Acknowledgements

Architecture concepts are inspired by Anthropic's Claude Code CLI. All Go code in this repository is original.
