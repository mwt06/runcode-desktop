# runcode

> An open-source AI coding companion CLI in Go.
> 中文名：**奔跑的代码** — see [README.zh-CN.md](./README.zh-CN.md).

[![CI](https://github.com/your-username/runcode/actions/workflows/ci.yml/badge.svg)](https://github.com/your-username/runcode/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/your-username/runcode.svg)](https://pkg.go.dev/github.com/your-username/runcode)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)

> **Status: v0.1-alpha scaffold.** The CLI builds and prints help, but the `chat` command is not wired yet. See the roadmap below.

## What is runcode?

`runcode` is an AI coding companion that lives in your terminal. It runs as a full-screen TUI (Bubble Tea) and drives a ReAct + Tool Use loop against an LLM provider — Anthropic Claude or OpenAI GPT — to read, write, edit, search, and execute code on your behalf, with explicit permission gates.

It is **inspired by** Anthropic's Claude Code (the official TS CLI), but is a clean-room Go reimplementation of the core ideas: streaming tool execution with concurrency partitioning, a cacheable system-prompt boundary, four-level permission modes, lifecycle hooks, MCP integration, and sub-agents.

## Quick start

```bash
git clone https://github.com/your-username/runcode.git
cd runcode
go build ./cmd/runcode
./runcode version
./runcode --help
```

> Requires Go 1.26+.

## Roadmap

| Version | Target | Focus |
|---------|--------|-------|
| **v0.1** | 4-6 weeks | TUI + Anthropic + 7 tools (Read/Write/Edit/Glob/Grep/Bash/TodoWrite) + default permissions + cacheable prompt boundary + SQLite transcript |
| v0.2 | +3 weeks | OpenAI provider + 4-level permissions + Hook system + slash commands + WebFetch/WebSearch |
| v0.3 | +4 weeks | Sub-agents (Explore / Plan / Verification) + context compaction + Skills + `runcode print` |
| v0.4 | +5 weeks | MCP integration + Coordinator multi-worker + plugin manifest |
| v1.0 | +4 weeks | Performance + i18n + GoReleaser multi-platform release + Homebrew/scoop/AUR |

See [docs/architecture.md](./docs/architecture.md) (when populated) for the full design.

## Architecture at a glance

```
User → Bubble Tea TUI → REPL Controller → ReAct loop ─┬─→ LLM Provider (Anthropic | OpenAI)
                              │                       │
                              │              ┌────────┴────────┐
                              ↓              │ Streaming Tool  │
                        Permission Engine ←──┤ Executor        │
                              │              │ (concurrent /   │
                              ↓              │  serial groups) │
                        Hook Chain          └─────────────────┘
```

Key Go-idiomatic translations of the TS source:

- `AsyncGenerator<Event>` → `chan<- Event` + goroutine
- `DeepImmutable AppState` → `atomic.Pointer[AppState]` + COW
- `useSyncExternalStore` → Store notify channel → `tea.Cmd` → `tea.Msg`
- `__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__` → string constant + provider-side `cache_control` injection
- Feature-flag DCE → runtime config + interface injection (single binary, no build-tag matrix)

## Project layout

```
cmd/runcode/           CLI entry (cobra)
internal/              Implementation details (not importable)
  app/                 Bubble Tea Model/Update/View
  repl/                ReAct controller + executor
  permissions/         4-level mode + rule engine
  prompt/              System-prompt assembler + boundary
  hooks/               Lifecycle hook chain
  mcp/                 MCP connection pool
  persistence/         SQLite + RUNCODE.md loader + settings
  session/  cost/  telemetry/  ui/
pkg/                   Stable SDK (semver-promised)
  tool/                Tool interface + Context
  llm/                 Provider abstraction + neutral message types
    providers/         anthropic/, openai/
  agent/  skill/  command/  plugin/
tools/                 Built-in tool implementations + registry
prompts/               //go:embed templates
```

## Configuration

Per-project context goes in `RUNCODE.md` at the repo root (legacy `CLAUDE.md` is also read for compatibility). Schema and details will land in v0.2.

## Contributing

This project is in **alpha**. The `pkg/` SDK is **not stable** until v1.0. See [CONTRIBUTING.md](./CONTRIBUTING.md).

## License

Apache-2.0 — see [LICENSE](./LICENSE).

## Acknowledgements

Architecture concepts derived from Anthropic's Claude Code CLI (TypeScript). All Go code is original.
