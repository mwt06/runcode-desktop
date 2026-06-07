# runcode

> An open-source AI coding companion CLI in Go.
> 中文名：**奔跑的代码** — see [README.zh-CN.md](./README.zh-CN.md).

[![CI](https://github.com/wt68/runcode/actions/workflows/ci.yml/badge.svg)](https://github.com/wt68/runcode/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wt68/runcode.svg)](https://pkg.go.dev/github.com/wt68/runcode)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)

> **Status: v0.1-alpha.** The project has a minimal provider-backed `chat` command, an in-memory ReAct loop, a minimal Bubble Tea `tui` command, safe/interactive permissions for CLI chat, telemetry, and built-in `Read`/`Write`/`Edit`/`Glob`/`Grep`/`Bash` tools. It is not a full TUI product yet.

## What is runcode?

`runcode` is a Go implementation of an AI coding companion for the terminal. The current build is intentionally small: it runs a shell-friendly `runcode chat` command backed by Anthropic, exposes a bounded set of local tools, and gates file mutations and command execution through an internal permission layer.

The long-term direction is inspired by Anthropic's Claude Code, but this repository is an original Go implementation. The Bubble Tea TUI is currently a minimal MVP; many planned systems — hooks, sub-agents, SQLite transcripts, richer TUI permissions/tool UI, and broader multi-provider support — are still scaffolds or deferred work.

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

ANTHROPIC_MODEL=claude-sonnet-4-6 \
ANTHROPIC_API_KEY=... \
./runcode tui
```

`runcode chat` streams assistant text deltas to stdout as they arrive. `runcode tui` starts a minimal Bubble Tea interface with a Claude Code-style bottom status area, cumulative context token and thinking-mode indicators, scrollable conversation viewport, a multi-line input with top and bottom dividers (Enter sends; `alt+enter`/`ctrl+j` insert a newline; ↑/↓ recall submitted-input history or move the cursor inside a multi-line draft), streaming assistant Markdown rendering, minimal tree-style tool progress cards with safe file summaries, and slash commands (`/help`, `/clear`, `/status`, `/mode`, `/model`, `/compact`, `/cost`, `/exit`) with a type-to-filter menu that opens when you type `/`. With `--permission-mode interactive` the TUI shows a permission modal offering allow once / allow for session / deny; "allow for session" stops re-prompting equivalent actions for the rest of the session. Tool cards show a bounded, sanitized output excerpt (Bash stdout/stderr, Grep matches, Read preview) and a full line diff for Edit/Write, expandable with `ctrl+o`.

Useful flags and environment variables:

- `--provider` / `RUNCODE_PROVIDER`: `anthropic` or `openai` (the latter also drives OpenAI-compatible endpoints such as vLLM/Ollama/llama.cpp/gateways; point `--base-url` at the API root serving `/chat/completions`, and the bearer credential is optional for unauthenticated local endpoints).
- `--model` / `ANTHROPIC_MODEL`: required unless provided by environment.
- `--api-key` / `ANTHROPIC_API_KEY`, or `--auth-token` / `ANTHROPIC_AUTH_TOKEN`.
- `--base-url` / `ANTHROPIC_BASE_URL`.
- `--max-retries` / `RUNCODE_MAX_RETRIES`: provider transient-failure retries (0 = default, negative = disabled).
- `--input-price` / `--output-price` (`RUNCODE_INPUT_PRICE` / `RUNCODE_OUTPUT_PRICE`): token prices per million, for the TUI `/cost` estimate. When unset, the price is looked up from a built-in table by model name (Claude 4.x family and common OpenAI models); an explicit price always wins, and an unknown model (e.g. a self-hosted endpoint) stays unpriced. Built-in prices are approximate — set explicit prices for billing-grade accuracy.
- `--cwd` / `RUNCODE_CWD`: workspace for tools.
- `--loop`: keep one in-memory session alive across stdin prompts; use `/clear` to reset that in-memory history.
- `--max-history-messages` / `RUNCODE_MAX_HISTORY_MESSAGES`: bound how many in-memory history messages are sent to the provider each turn (`0` = unlimited, the default). Trimming keeps the current turn intact, never splits `tool_use`/`tool_result` pairs, and does not touch transcript files.
- `--permission-mode safe|interactive` / `RUNCODE_PERMISSION_MODE`.
- `--telemetry off|jsonl` / `RUNCODE_TELEMETRY`.
- `--transcript off|jsonl` / `RUNCODE_TRANSCRIPT`: optionally write JSONL transcripts under `<workspace>/.runcode/transcripts/`.
- `--session-id` / `RUNCODE_SESSION_ID`: choose the transcript file name when transcript recording is enabled.

### Configuration files

runcode also reads TOML config files, with precedence **flag > env > project file > user file > default**:

- Project: `runcode.toml`, discovered by walking up from the working directory.
- User: `config.toml` under `os.UserConfigDir()/runcode/` (`%AppData%\runcode\config.toml` on Windows, `~/.config/runcode/config.toml` on Linux, `~/Library/Application Support/runcode/config.toml` on macOS).

Supported keys: `provider`, `model`, `base_url`, `max_tokens`, `permission_mode`, `telemetry`, `transcript`, `max_history_messages`, and — **user file only** — `api_key` / `auth_token` and `[mcp.servers.*]` (see below). Credentials in a project file are ignored so they are never committed by accident.

### MCP servers (Model Context Protocol)

runcode can connect to MCP servers and expose their tools to the model. Servers are configured under `[mcp.servers.<name>]` in the **user-level** `config.toml` only — a project file can never make runcode launch a subprocess or reach an endpoint just by being opened. Each server is reached over **stdio** (a local subprocess) or **Streamable HTTP** (a remote endpoint). String values support `${VAR}` expansion so secrets stay in environment variables rather than the file.

```toml
# stdio: a local server launched as a subprocess (default transport)
[mcp.servers.filesystem]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/expose"]
env = { SOME_TOKEN = "${SOME_TOKEN}" }   # ${VAR} expanded from the environment
# enabled = false                         # omit or set true to enable

# http: a remote server over Streamable HTTP
[mcp.servers.docs]
transport = "http"
url = "https://example.com/mcp"
headers = { Authorization = "Bearer ${DOCS_TOKEN}" }
```

A server's tools appear to the model as `mcp__<server>__<tool>`. They are classified as an **external** permission operation: every call requires approval (so safe mode denies them), and "allow for session/project" remembers a grant per server+tool. A server that fails to connect is reported as a warning and skipped — it never aborts the session. Server names must use letters, digits, `-`, or `_` and contain no `__`.

If a connected server advertises the **resources** capability, two built-in tools are exposed: `ListMcpResources` (list each server's resources — uri, name, description — optionally filtered to one server) and `ReadMcpResource` (read a resource's contents by server + uri). Inline text passes through; binary contents are noted as a placeholder. Likewise, a server advertising **prompts** gets `ListMcpPrompts` and `GetMcpPrompt` (render a prompt template by server + name with a string-argument map). Like server tool calls, all four are external operations requiring approval; a read/get grant is remembered per server + uri/name. These tools appear only when a connected server supports the matching capability.

runcode also exposes **roots**: it advertises the workspace to servers and answers `roots/list` with the workspace as a `file://` root (and answers `ping`), so a server such as a filesystem server can learn the directory runcode operates in.

**Sampling** (a server requesting a model completion from runcode) is **off by default** — it spends your model on the server's behalf. Enable it with `--allow-mcp-sampling` (or `RUNCODE_ALLOW_MCP_SAMPLING`, or `allow_sampling` under `[mcp]` in the user config); even then, safe mode always refuses. When enabled, runcode serves `sampling/createMessage` with its configured provider/model, sending only the server-supplied messages and system prompt (never your conversation), declaring no tools, and bounding `maxTokens`. Enabling is the pre-authorization; there is no per-request prompt yet. MCP resource templates and subscriptions are not implemented yet.

### Skills

A **skill** is a reusable workflow: a directory holding a `SKILL.md` whose frontmatter declares a `name` and `description` and whose body is detailed instructions. Skills are discovered from two convention directories — `<userConfigDir>/runcode/skills/` (user-level) and `<workspace>/.runcode/skills/` (project-level) — with a user skill shadowing a project skill of the same name. No configuration is needed; just drop a directory in.

```text
~/.config/runcode/skills/code-review/SKILL.md   # or %AppData%\runcode\skills\... on Windows
```

```markdown
---
name: code-review
description: Review the current diff for correctness and clarity
---

1. Read the diff with `git diff`.
2. Check each change for ...
```

To keep context cheap, only a compact **catalog** (each skill's name and description, with a `[project]` tag for workspace skills) is injected into the system prompt. The model loads a skill's full instructions on demand by calling the built-in **`Skill`** tool with its name — so unused skills cost no context. The `Skill` tool only returns in-memory text (it launches nothing and touches no files), so it runs without approval. A malformed or oversized skill is skipped with a warning rather than breaking the session. Project skills are honored so teams can share workflows in-repo, but their text enters the prompt, so they are attributed as `[project]`. `runcode config` lists the loaded skill names (project ones tagged) without printing any body.

Run `runcode config` to print the effective configuration and which files were loaded (credential values are never printed).

```toml
# runcode.toml
model = "claude-opus-4-8"
base_url = "https://api.anthropic.com"
permission_mode = "interactive"
```

### Session resume & compaction

runcode saves each session's full conversation to `<workspace>/.runcode/sessions/<id>.jsonl` by default, so you can continue where you left off:

- `--resume <id>`: restore a saved session and continue it.
- `--continue`: resume the most recent session in this workspace.
- `--no-session` / `RUNCODE_SESSION_PERSIST=off`: disable history persistence.

Set `--max-context-tokens` (or `max_context_tokens` in config) to cap context: when a turn's input tokens approach the budget, runcode summarizes the oldest turns into one message and keeps recent turns verbatim. Compaction affects only the in-memory working set — the on-disk history stays complete. `/clear` clears the in-memory context, but the on-disk session log remains a full append-only record.

The session log is loss-less and may contain file contents and command output; it is written `0600` inside the workspace and ignored via `.gitignore`.

Current limitations:

- TUI is MVP-only: it has an interactive permission modal, rich tool output (output excerpts plus Edit/Write line diffs), and a growing multi-line input with submitted-input history recall, but no file tree, transcript browser, or syntax highlighting yet.
- No transcript-backed session resume; JSONL transcripts are append-only and opt-in.
- Slash commands run on an extensible registry (built-ins `/help`, `/clear`, `/status`, `/mode`, `/model`, `/compact`, `/cost`, `/exit`; `/mode safe|interactive` switches permission mode and `/model <name>` switches the model, both at runtime). MCP tools are supported (stdio + Streamable HTTP, the tools primitive — see [MCP servers](#mcp-servers-model-context-protocol)), as are skills (progressive disclosure via the `Skill` tool — see [Skills](#skills)) and MCP resources/prompts (`ListMcpResources`/`ReadMcpResource`, `ListMcpPrompts`/`GetMcpPrompt`), roots, and opt-in sampling; no hooks or sub-agents yet.

## Implemented tools

Built-in tools are registered in `tools.Builtins()` and exposed to both the model tool spec and prompt tool summary:

| Tool | Current effect |
|------|----------------|
| `Read` | Reads workspace files with line numbers and records complete/partial read metadata. |
| `Write` | Creates files or overwrites fresh-read files inside the workspace. |
| `Edit` | Performs exact string replacement on fresh-read files inside the workspace. |
| `Glob` | Finds workspace files with slash glob patterns and `**`; concurrency-safe with sibling safe tool calls. |
| `Grep` | Searches workspace text files with Go regular expressions; concurrency-safe with sibling safe tool calls. |
| `Bash` | Runs a single-line non-interactive bash command in the workspace after permission approval. |
| `TodoWrite` | Records the current task list (content/status/activeForm per item); side-effect-free and allowed without approval. |
| `WebFetch` | Fetches an http(s) URL and returns its text (HTML reduced to plain text); a network operation that requires approval (shown per host). |

MCP server tools are also exposed dynamically as `mcp__<server>__<tool>` when configured (see [MCP servers](#mcp-servers-model-context-protocol)), and the `Skill` tool loads reusable workflows on demand (see [Skills](#skills)). WebSearch and plugin tools are not implemented yet.

## Permissions and safety

The executor calls `internal/permissions` before running every tool:

- Workspace `Read`/`Glob`/`Grep` are allowed by default.
- `Write`/`Edit` mutations require approval and fresh-read checks.
- `Bash` commands are classified before execution; unknown, privileged, destructive, outside-write, and complex shell-control commands are denied before approval.
- `WebFetch` (network) and MCP server tools (external) always require approval; "allow for session/project" remembers a grant per host and per server+tool respectively.
- `safe` mode is non-interactive, so approval-requiring actions resolve to denial.
- `interactive` mode asks once on stderr and only for actions already classified as approvable. Approval offers allow once / allow for session / allow for project; "allow for project" persists to `<workspace>/.runcode/permissions.json` (0600, gitignored) and is honored across processes. That file also holds a denylist checked before prompting (a deny always wins over an allow); a corrupt file fails fast rather than dropping deny rules.

`runcode permissions` manages that file without hand-editing JSON: `permissions list` numbers the persisted allow/deny rules, `permissions remove <n>` deletes one by number (works for every grain, including the mutation/command rules the TUI writes), and `permissions clear [--allow|--deny]` wipes them. `permissions deny <host>` / `permissions allow <host>` add a network rule for a host (default tool `WebFetch`) — the one rule kind that is reliably typeable and matches exactly; a deny always wins, so allowing a denied host is refused until the deny is removed.

Telemetry records bounded metadata such as operation, risk, resource scope, permission effect, and command classification. It does not record raw paths, raw commands, tool inputs, tool outputs, file contents, credentials, or URLs.

Transcript recording is off by default. When enabled with `--transcript jsonl`, runcode writes append-only turn records to `<workspace>/.runcode/transcripts/<session-id>.jsonl`; records include user text, final assistant text, bounded tool summaries, and Bash command strings, but not system prompts, credentials, generic tool raw input, or full tool output.

## Architecture at a glance

```text
User input
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

See:

- [docs/architecture.md](./docs/architecture.md) for the implemented architecture.
- [docs/data-flow-and-prompt.md](./docs/data-flow-and-prompt.md) for the request/tool/prompt data flow.
- [docs/implementation-status.md](./docs/implementation-status.md) for current gaps and intentionally minimal areas.

## Project layout

```text
cmd/runcode/           Cobra CLI: version, chat, and minimal tui
internal/ui/           Bubble Tea TUI MVP: bottom status area, viewport, input, Markdown rendering, tool progress/file summaries, slash commands
internal/repl/         ReAct session, executor, tool result conversion, telemetry
internal/permissions/  action/resource/risk model, policy, approval, command classification
internal/mcp/          Model Context Protocol client: JSON-RPC, stdio + HTTP transports, tool adapter, manager
internal/prompt/       system prompt assembler and cache boundary
internal/telemetry/    event model, JSONL, async, memory recorders
internal/persistence/  opt-in JSONL transcript recording
internal/toolpath/     workspace path resolution and fresh-read gates
pkg/tool/              public tool interface, schema, context, result types
pkg/llm/               provider-neutral LLM DTOs and stream interfaces
tools/                 built-in tools and registry
docs/                  current architecture, data flow, handoff, and status notes
```

Scaffolded but not implemented yet: `internal/hooks`, SQLite transcript persistence, `pkg/agent`, `pkg/command`, `pkg/plugin`, and `prompts/agents` / `prompts/templates`.

## Contributing

This project is in **alpha**. The `pkg/` SDK is **not stable** until v1.0. See [CONTRIBUTING.md](./CONTRIBUTING.md).

## License

Apache-2.0 — see [LICENSE](./LICENSE).

## Acknowledgements

Architecture concepts are inspired by Anthropic's Claude Code CLI. All Go code in this repository is original.
