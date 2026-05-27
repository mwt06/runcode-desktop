# runcode

> An open-source AI coding companion CLI in Go.
> 中文名：**奔跑的代码** — see [README.zh-CN.md](./README.zh-CN.md).

[![CI](https://github.com/wt68/runcode/actions/workflows/ci.yml/badge.svg)](https://github.com/wt68/runcode/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wt68/runcode.svg)](https://pkg.go.dev/github.com/wt68/runcode)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)

> **Status: v0.1-alpha.** The project has a minimal provider-backed `chat` command, an in-memory ReAct loop, safe/interactive permissions, telemetry, and built-in `Read`/`Write`/`Edit`/`Glob`/`Grep`/`Bash` tools. It is not a full TUI product yet.

## What is runcode?

`runcode` is a Go implementation of an AI coding companion for the terminal. The current build is intentionally small: it runs a shell-friendly `runcode chat` command backed by Anthropic, exposes a bounded set of local tools, and gates file mutations and command execution through an internal permission layer.

The long-term direction is inspired by Anthropic's Claude Code, but this repository is an original Go implementation. Many planned systems — Bubble Tea TUI, MCP, hooks, sub-agents, skills, SQLite transcripts, context compaction, and multi-provider support — are still scaffolds or deferred work.

## Quick start

```bash
git clone https://github.com/wt68/runcode.git
cd runcode
go build ./cmd/runcode
./runcode version
./runcode --help
```

> Requires Go 1.26+.

## Current CLI

```bash
ANTHROPIC_MODEL=claude-sonnet-4-6 \
ANTHROPIC_API_KEY=... \
./runcode chat "summarize this repository"
```

Useful flags and environment variables:

- `--provider` / `RUNCODE_PROVIDER`: currently only `anthropic`.
- `--model` / `ANTHROPIC_MODEL`: required unless provided by environment.
- `--api-key` / `ANTHROPIC_API_KEY`, or `--auth-token` / `ANTHROPIC_AUTH_TOKEN`.
- `--base-url` / `ANTHROPIC_BASE_URL`.
- `--cwd` / `RUNCODE_CWD`: workspace for tools.
- `--loop`: keep one in-memory session alive across stdin prompts.
- `--permission-mode safe|interactive` / `RUNCODE_PERMISSION_MODE`.
- `--telemetry off|jsonl` / `RUNCODE_TELEMETRY`.

Current limitations:

- No Bubble Tea TUI.
- No persistent transcript or session resume.
- No streaming terminal rendering; final assistant text is printed after each turn.
- No slash commands, MCP, hooks, sub-agents, skills, or OpenAI provider yet.

## Implemented tools

Built-in tools are registered in `tools.Builtins()` and exposed to both the model tool spec and prompt tool summary:

| Tool | Current effect |
|------|----------------|
| `Read` | Reads workspace files with line numbers and records complete/partial read metadata. |
| `Write` | Creates files or overwrites fresh-read files inside the workspace. |
| `Edit` | Performs exact string replacement on fresh-read files inside the workspace. |
| `Glob` | Finds workspace files with slash glob patterns and `**`. |
| `Grep` | Searches workspace text files with Go regular expressions. |
| `Bash` | Runs a single-line non-interactive bash command in the workspace after permission approval. |

`TodoWrite`, WebFetch/WebSearch, MCP tools, and plugin tools are not implemented yet.

## Permissions and safety

The executor calls `internal/permissions` before running every tool:

- Workspace `Read`/`Glob`/`Grep` are allowed by default.
- `Write`/`Edit` mutations require approval and fresh-read checks.
- `Bash` commands are classified before execution; unknown, privileged, destructive, outside-write, and complex shell-control commands are denied before approval.
- `safe` mode is non-interactive, so approval-requiring actions resolve to denial.
- `interactive` mode asks once on stderr and only for actions already classified as approvable.

Telemetry records bounded metadata such as operation, risk, resource scope, permission effect, and command classification. It does not record raw paths, raw commands, tool inputs, tool outputs, file contents, credentials, or URLs.

## Architecture at a glance

```text
User input
  -> cmd/runcode chat
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

See:

- [docs/architecture.md](./docs/architecture.md) for the implemented architecture.
- [docs/data-flow-and-prompt.md](./docs/data-flow-and-prompt.md) for the request/tool/prompt data flow.
- [docs/implementation-status.md](./docs/implementation-status.md) for current gaps and intentionally minimal areas.

## Project layout

```text
cmd/runcode/           Cobra CLI: version and minimal chat
internal/repl/         ReAct session, executor, tool result conversion, telemetry
internal/permissions/  action/resource/risk model, policy, approval, command classification
internal/prompt/       system prompt assembler and cache boundary
internal/telemetry/    event model, JSONL, async, memory recorders
internal/toolpath/     workspace path resolution and fresh-read gates
pkg/tool/              public tool interface, schema, context, result types
pkg/llm/               provider-neutral LLM DTOs and stream interfaces
tools/                 built-in tools and registry
docs/                  current architecture, data flow, handoff, and status notes
```

Scaffolded but not implemented yet: `internal/ui`, `internal/mcp`, `internal/hooks`, `internal/persistence`, `internal/compaction`, `internal/cost`, `pkg/agent`, `pkg/skill`, `pkg/command`, `pkg/plugin`, `pkg/llm/providers/openai`, `tools/todo`, and `prompts/*`.

## Contributing

This project is in **alpha**. The `pkg/` SDK is **not stable** until v1.0. See [CONTRIBUTING.md](./CONTRIBUTING.md).

## License

Apache-2.0 — see [LICENSE](./LICENSE).

## Acknowledgements

Architecture concepts are inspired by Anthropic's Claude Code CLI. All Go code in this repository is original.
