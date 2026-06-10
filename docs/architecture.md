# runcode Architecture

This document tracks the architecture that is currently implemented in runcode.

## Current status

runcode is a v0.1-alpha scaffold. It has the core package boundaries needed for an AI coding companion, and the provider-neutral session controller now supports a finite ReAct loop with in-memory multi-turn history. The CLI chat command is wired as a minimal provider-backed command with an optional loop mode; `runcode tui` is wired as a minimal Bubble Tea MVP for a Claude Code-style bottom status area with cumulative context token and thinking-mode indicators, conversation, input, streaming assistant Markdown rendering, tree-style tool progress cards with safe file summaries, and basic slash commands.

Implemented:

- `cmd/runcode`: Cobra CLI entry point with `version`, a minimal provider-backed `chat` command with optional in-memory `--loop` mode, and a minimal `tui` command.
- `internal/ui`: Bubble Tea TUI MVP with a Claude Code-style bottom status area, cumulative context token and thinking-mode indicators, scrollable conversation viewport, multi-line input with readline-style history recall, streaming assistant Markdown rendering, tree-style tool progress cards with sanitized output summaries and Edit/Write line diffs, an interactive permission modal (allow once / session / project / deny), and slash commands `/help` / `/clear` / `/compact` / `/status` / `/mode` / `/model` / `/cost` / `/exit`.
- `pkg/tool`: public tool SDK boundary, including tool context, schema, events, and result types.
- `pkg/llm`: provider-neutral LLM request, message, stream, content block, cache, and tool spec types.
- `tools/registry.go`: single registration point for built-in tools.
- `tools/read`: built-in read tool, returning line-numbered file content and updating `tool.Context.ReadSet` with complete/partial read metadata.
- `tools/write`: built-in whole-file write tool, supporting create and overwrite while requiring a fresh complete read before overwriting existing files.
- `tools/edit`: built-in exact replace tool, requiring a fresh complete read and unique `old_string` unless `replace_all` is set.
- `tools/glob`: built-in workspace file discovery tool with cross-platform slash glob semantics and `**` support.
- `tools/grep`: built-in workspace text search tool using Go regexp patterns, optional slash glob filtering, and bounded output.
- `tools/bash`: minimal non-interactive Bash executor, fixed to the workspace root with timeout and output bounds, gated by command permission classification before execution.
- `internal/repl`: finite ReAct session controller, permission-aware tool executor, tool result conversion, and tool-spec conversion for future model tool exposure.
- `internal/permissions`: internal permission boundary with action/resource/risk modeling, default safe policy, non-interactive and interactive authorizers, approval model, and permission telemetry helpers.
- `internal/telemetry`: internal observability foundation with event model, no-op recorder, bounded async recorder, memory recorder, stderr JSONL support, and permission decision events.
- `internal/persistence/transcript`: opt-in append-only JSONL transcript recorder with whitelisted turn summaries.
- `internal/prompt`: system prompt boundary, assembler, and static/dynamic prompt sections.
- `pkg/llm/providers/anthropic`: Anthropic SDK-backed provider skeleton with request and stream conversion tests.
- `internal/hooks`: user-configured lifecycle commands (PreToolUse/PostToolUse/UserPromptSubmit) run around tool execution and prompt submission.
- `pkg/skill`: convention-directory skills with progressive disclosure — a compact catalog in the prompt and a `Skill` tool that loads a skill body on demand.
- `pkg/agent` + `internal/subagent`: sub-agents delegated via the built-in `Task` tool, each running a restricted child `repl.Session` with its own persona prompt and optional model; delegation is one level deep (sub-agents never receive `Task`), and children share the parent permission service and tool hooks.
- `pkg/memory`: persistent cross-session memory. The `Remember` tool appends a de-duplicated fact to the user/project memory file (fixed paths, never a caller path, so it is `manage`-classified), and saved facts are injected into the prompt's `Memory` section at startup.
- `tools/todo`: side-effect-free `TodoWrite` task-list tool; the model replaces the full list each call and the UI shows a progress summary.
- `tools/webfetch`: `WebFetch` http(s)-to-text fetch tool, gated as a `network` operation requiring per-host approval.
- `internal/mcp`: MCP client (stdio + Streamable HTTP) covering tools, resources, prompts, roots, and opt-in sampling.
- `pkg/llm/providers/openai`: OpenAI-compatible Chat Completions provider with SSE streaming and connection-phase retry/backoff.
- `internal/compaction`: token-budget-triggered semantic history compaction (LLM-summarized oldest turns, incremental summaries).
- `internal/persistence/sessions`: append-only full-history JSONL session store backing `--resume` / `--continue`.
- `internal/persistence/settings`: TOML settings loader (project `runcode.toml` + user `config.toml`, flag > env > project > user precedence).
- `internal/cost`: built-in model pricing table and token cost estimation behind `/cost`.
- `internal/hooks` configuration is user-level only; tool hooks run for built-in, MCP, and skill tools.

Not implemented yet:

- Full TUI product features beyond the MVP: diff viewer, file tree, transcript browser, and syntax highlighting (permission modal, rich tool output cards, multi-line input, and runtime model switching are implemented).
- User-level (global) persistent permission policy (project-level allow/deny rules persist in `<workspace>/.runcode/permissions.json` and are managed via `runcode permissions`).
- SQLite persistence and a transcript/session browser (resume itself is implemented via the `internal/persistence/sessions` JSONL store; MCP tools/resources/prompts/roots/sampling, compaction, hooks, skills, and sub-agents are implemented).
- Built-in tools beyond the current eight (`Read`, `Write`, `Edit`, `Glob`, `Grep`, `Bash`, `TodoWrite`, `WebFetch`), e.g. WebSearch.
- Bash background tasks, streaming terminal output, custom cwd/env, and interactive stdin.

## Current data flow

`tools.Builtins()` is the single source of truth for built-in tool availability.

```text
tools.Builtins()
  ├─> repl.NewExecutor() -> Executor.Execute() -> Tool.Run() -> tool.Result
  ├─> repl.ToolSpecs() -> []llm.ToolSpec
  └─> prompt.BuildSystemPrompt() -> sections.UsingTools()

Session.RunTurn()
  ├─> clone in-memory history + append current user message
  ├─> prompt.BuildSystemPrompt()
  ├─> repl.ToolSpecs()
  ├─> llm.Provider.Stream()
  ├─> Executor.Execute()
  │   ├─> permissions.Service.AuthorizeTool()
  │   ├─> allowed -> Tool.Run()
  │   └─> denied -> error tool.Result without running tool
  ├─> ToolResultBlock()
  ├─> repeat provider/tool steps until no tool_use or max iterations
  ├─> successful turn commits messages back to in-memory history
  └─> optional transcript.Recorder writes a JSONL turn summary
```

This keeps tool implementation, tool execution, model-facing tool schemas, and prompt-visible tool descriptions aligned without coupling those consumers to concrete tool packages. The current session controller runs a bounded provider/tool loop within one user turn: it appends assistant `tool_use` messages and tool result messages back into the next provider request until the assistant stops requesting tools or the maximum iteration count is reached. Successful turns are stored as in-memory `llm.Message` history on the session; failed turns are not committed or written to transcript.

## CLI boundary

`runcode chat` is wired as a minimal non-TUI command. By default it accepts a single prompt from args or stdin, constructs the Anthropic provider, built-in tools, prompt assembler inputs, telemetry recorder, and `internal/repl.Session`, then prints the final assistant text to stdout.

`runcode chat --loop` keeps one process-local session alive across prompts. Args become the first prompt, subsequent prompts are read line-by-line from stdin, and EOF or `/exit` / `/quit` / `exit` / `quit` exits cleanly. `/clear` resets only the in-memory history. The loop does not start a Bubble Tea UI, resume transcript-backed sessions, or compact history.

`runcode tui` starts a Bubble Tea UI. It reuses the same chat config flags and session construction path, but routes `SessionOptions.StreamDelta` into Bubble Tea messages instead of writing deltas directly to stdout. In `--permission-mode interactive` it shows a permission modal (allow once / allow session / allow project / deny) with session-level memory and project-level persistence; slash commands (`/help`, `/clear`, `/compact`, `/status`, `/mode`, `/model`, `/cost`, `/exit`) are handled locally.

The `chat` command remains shell-friendly. Assistant text is written to stdout. Loop prompt markers, interactive permission approval, and `RUNCODE_TELEMETRY=jsonl` / `--telemetry jsonl` output are written to stderr. Loop prompt input and approval input share the same line reader so they do not lose buffered stdin data. `RUNCODE_TRANSCRIPT=jsonl` / `--transcript jsonl` writes append-only records to `<workspace>/.runcode/transcripts/<session-id>.jsonl`; `--session-id` / `RUNCODE_SESSION_ID` can choose the file name.

## Prompt cache boundary

`internal/prompt.BuildSystemPrompt` produces text content blocks split into cacheable static sections and non-cacheable dynamic sections.

Static sections currently include:

- intro text
- system behavior text
- built-in tool descriptions
- action guidance
- tone/style guidance
- `prompt.DynamicBoundary`

Dynamic sections currently include:

- per-turn selected reasoning guidance from the optional AI classification request
- current working directory
- current date
- shell info
- permission mode guidance
- memory
- project context

The selected reasoning guidance is non-cacheable because it is chosen per user turn. The built-in tool descriptions are cacheable because the current built-in tool set is fixed and comes from `tools.Builtins()`. If future work makes tools dynamic through MCP, permissions, plugins, workspace configuration, or session state, the cache strategy must be reviewed. The likely options are moving tool descriptions into the dynamic section or making cache keys account for the effective tool set.

## Permission boundary

`internal/permissions` is the single authorization layer for tool execution. `internal/repl.Executor` resolves every tool call into an action, asks the permission service for a decision, records a sanitized permission telemetry event, and only then runs the tool.

Current default mode is `safe` and non-interactive:

- workspace-scoped `Read`, `Glob`, and `Grep` are allowed.
- `Read`, `Glob`, and `Grep` outside the configured working directory are denied.
- `Write` create and fresh-read overwrite inside the workspace are modeled as approval-requiring; in `safe` mode they resolve to denial.
- `Edit` exact replace inside the workspace is modeled as approval-requiring only when the target has a fresh complete read; in `safe` mode it resolves to denial.
- `Write`/`Edit` outside the workspace, missing read state, partial reads, stale reads, invalid targets, unknown tools, and unknown operations are denied.
- `Bash` command execution operations are pre-classified into controlled command categories, capabilities, risk reasons, and a bounded summary before policy evaluation.
- `Bash` is not auto-allowed in any current default policy path; known non-critical commands require approval, while unknown, privileged, outside-write, destructive VCS, and complex shell-control commands are denied before approval.
- The Bash runtime has a second safety boundary: fixed workspace cwd, no stdin, no custom env, bounded timeout, bounded stdout/stderr capture, and no background task management.

`interactive` mode (`--permission-mode interactive` or `confirm`, or `RUNCODE_PERMISSION_MODE=interactive`) installs an `InteractiveAuthorizer`. It only handles policy decisions that are already `ask`, prompts for allow-once/deny-once on stderr, and safely denies on EOF, context cancellation, prompt errors, or too many invalid answers. Approval does not bypass resolver, policy, workspace containment, fresh-read, or tool runtime safety gates.

Permission denial is returned to the model as a tool result with `is_error=true`; it does not interrupt the whole turn. This keeps tool success, tool failure, and permission denial on the same ReAct path while reserving Go errors for internal failures.

Permission telemetry records only action metadata such as operation, risk, effect, reason, resource type/count/scope, command category/capabilities/risk reasons/summary, and correlation IDs. It does not record raw paths, raw commands, raw command arguments, tool input, tool output, prompt text, file contents, credentials, URLs, or base URLs. Tool execution error telemetry also uses a bounded error category instead of raw error text so file paths are not emitted.

Path resolution is shared through `internal/toolpath` so tool execution and permission resolution use the same base-directory semantics. Permission containment checks resolve existing symlink targets before deciding whether a read or mutation is inside the workspace. Mutation target resolution treats existing targets and missing targets differently: existing targets check the real target path, while missing targets require an existing real parent directory inside the workspace. Overwrite/edit operations also require `tool.Context.ReadSet` to contain a complete read whose size and modification time still match the current file.

## Observability boundary

`internal/telemetry` defines the internal observability event model. Session and executor code record lifecycle events through a narrow `Recorder` interface, while concrete output sinks stay in the telemetry package.

Current events cover:

- turn lifecycle: `turn.start`, `turn.end`, `turn.error`.
- LLM request lifecycle: `llm.request.start`, `llm.request.end`, `llm.request.error`.
- tool execution lifecycle: `tool.execute.start`, `tool.execute.end`, `tool.execute.error`.
- permission decisions: `permission.decision`.

Events include correlation IDs (`trace_id`, `turn_id`, `request_id`, `tool_use_id`) and bounded metadata such as counts, durations, stop reasons, token usage, and error strings. They intentionally exclude prompts, assistant text, tool inputs, tool outputs, file contents, credentials, and base URLs.

Runtime telemetry recorders are no-op by default. JSONL telemetry mode uses a bounded async recorder so telemetry IO does not block the chat/session/tool path; a full queue drops events rather than slowing the agent.

## Transcript boundary

Transcript recording is opt-in through `--transcript jsonl` or `RUNCODE_TRANSCRIPT=jsonl`. The JSONL recorder writes one whitelisted turn summary per successful turn under `<workspace>/.runcode/transcripts/<session-id>.jsonl`; failed turns are not written. If no session id is provided, runcode generates one. `/clear` only resets in-memory history and does not delete or rotate the transcript file.

Transcript records include user text, final assistant text, stop reason, token usage, iteration count, tool call id/name summaries, Bash command strings, and bounded tool result counts/byte sizes. They intentionally exclude system prompts, provider requests, credentials, base URLs, generic tool raw input, full tool output, thinking content, and image data. Transcripts are not used for resume; session resume is backed by the separate full-history store in `internal/persistence/sessions` (`.runcode/sessions/<id>.jsonl`, on by default) together with `--resume` / `--continue`, and over-budget working history is compacted by `internal/compaction`.

## Provider-neutral LLM model

`pkg/llm` defines in-process neutral DTOs, not a provider wire schema. Provider adapters translate these types into provider-specific payloads and streams while keeping SDK details out of `internal/repl`.

`pkg/llm/providers/anthropic` is the first provider adapter. It uses the official Anthropic Go SDK internally, converts neutral requests into Anthropic Messages requests, and normalizes SDK stream events back into `llm.StreamEvent`. The minimal `cmd/runcode chat` command uses this provider for single-turn CLI execution.

`llm.ContentBlock` already represents the structures needed for future tool use:

- `tool_use`: model-requested tool call with ID, name, and raw JSON input.
- `tool_result`: nested content tied back to the original tool use ID.
- `text`, `thinking`, and `image` blocks.
- cache control hints for provider prompt caching.

## Verification matrix

The current scaffold should be validated through tests rather than through a real chat session.

| Area | What is verified |
|------|------------------|
| `tools` | built-in tool list, unique names, tool metadata, concurrency-safety expectations, and current `Read`/`Write`/`Edit`/`Glob`/`Grep`/`Bash`/`TodoWrite`/`WebFetch` registration |
| `tools/read` | file reading, line numbering, offset/limit behavior, relative paths, complete/partial `ReadSet`, errors, CRLF, cancellation, and output bounds |
| `tools/write` | file creation, fresh-read overwrite, missing/partial/stale read rejection, workspace containment, and missing parent rejection |
| `tools/edit` | exact replace, replace-all behavior, unique match enforcement, invalid input rejection, fresh-read requirement, and workspace containment |
| `tools/glob` | slash glob matching, recursive `**`, stable workspace-relative output, limit truncation, cancellation, and workspace containment |
| `tools/grep` | regexp search, case-insensitive mode, slash glob filtering, file/directory search, binary skip, limit truncation, cancellation, and workspace containment |
| `tools/bash` | non-interactive execution, workspace cwd, stdout/stderr capture, non-zero exit error results, timeout/cancel handling, and output truncation |
| `internal/toolpath` | shared path resolution, workspace containment, symlink handling, mutation target resolution, and read freshness checks |
| `internal/repl` | session request construction, in-memory history commit/reset behavior, transcript recording on successful turns, stream collection, permission-aware executor behavior, interactive approval allow/deny execution paths, event channel forwarding, tool use ID propagation, and `tool_use` to `tool_result` conversion |
| `internal/permissions` | action/resource resolution, workspace containment, command classification, default safe policy, non-interactive and interactive authorization, approval fallback behavior, and sanitized decision data |
| `internal/prompt` | boundary behavior, static/dynamic ordering, cache policy, environment isolation, and tool description injection |
| `internal/telemetry` | event model, JSONL output, async flush/drop behavior, memory recorder, and ID generation |
| `internal/persistence/transcript` | JSONL append behavior, session id validation, and transcript sanitizer whitelist |
| `pkg/llm` | provider/stream interfaces and neutral content block contracts |
| `pkg/llm/providers/anthropic` | provider contract, request conversion, tool use/result mapping, stream event conversion, usage, stop reasons, and error/close behavior |
| `pkg/agent` | frontmatter parsing, tolerant directory loading, README skip, name precedence/dedup, tool-policy filtering, catalog rendering, and shipped example definitions |
| `internal/subagent` | launcher tool-set filtering, model override, persona/system-prompt assembly, `Task` input validation and agent resolution, hook scoping (tool hooks only, no UserPromptSubmit), and child progress event bridging |
| `pkg/memory` | scope rendering, bullet parsing, entry normalization, store load/append with case-insensitive de-dup, scope availability, truncation, and Remember tool validation |
| `cmd/runcode` | `version` output, chat prompt input, `chat --loop` behavior, config parsing, fake-runner output, approval prompt behavior, shared line input, runtime IO propagation, and error propagation |

Recommended validation commands:

```bash
go test ./...
go test -race ./...
go build ./cmd/runcode
```
