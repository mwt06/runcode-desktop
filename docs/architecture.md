# runcode Architecture

This document tracks the architecture that is currently implemented in runcode.

## Current status

runcode is a v0.1-alpha scaffold. It has the core package boundaries needed for an AI coding companion, and the provider-neutral session controller now supports a finite ReAct loop. The CLI chat command is wired as a minimal single-turn provider-backed command; the full interactive TUI is intentionally not wired yet.

Implemented:

- `cmd/runcode`: Cobra CLI entry point with `version` and a minimal single-turn `chat` command.
- `pkg/tool`: public tool SDK boundary, including tool context, schema, events, and result types.
- `pkg/llm`: provider-neutral LLM request, message, stream, content block, cache, and tool spec types.
- `tools/registry.go`: single registration point for built-in tools.
- `tools/read`: built-in read tool, returning line-numbered file content and updating `tool.Context.ReadSet` with complete/partial read metadata.
- `tools/write`: built-in whole-file write tool, supporting create and overwrite while requiring a fresh complete read before overwriting existing files.
- `tools/edit`: built-in exact replace tool, requiring a fresh complete read and unique `old_string` unless `replace_all` is set.
- `internal/repl`: finite ReAct session controller, permission-aware tool executor, tool result conversion, and tool-spec conversion for future model tool exposure.
- `internal/permissions`: internal permission boundary with action/resource/risk modeling, default safe policy, non-interactive authorizer, and permission telemetry helpers.
- `internal/telemetry`: internal observability foundation with event model, no-op recorder, bounded async recorder, memory recorder, stderr JSONL support, and permission decision events.
- `internal/prompt`: system prompt boundary, assembler, and static/dynamic prompt sections.
- `pkg/llm/providers/anthropic`: Anthropic SDK-backed provider skeleton with request and stream conversion tests.

Not implemented yet:

- Multi-turn interactive chat loop.
- Bubble Tea TUI.
- Interactive permission prompts and persistent policy configuration.
- MCP, hooks, sub-agents, skills, compaction, and persistence.
- Built-in tools beyond `Read`, `Write`, and `Edit`.

## Current data flow

`tools.Builtins()` is the single source of truth for built-in tool availability.

```text
tools.Builtins()
  ├─> repl.NewExecutor() -> Executor.Execute() -> Tool.Run() -> tool.Result
  ├─> repl.ToolSpecs() -> []llm.ToolSpec
  └─> prompt.BuildSystemPrompt() -> sections.UsingTools()

Session.RunTurn()
  ├─> prompt.BuildSystemPrompt()
  ├─> repl.ToolSpecs()
  ├─> llm.Provider.Stream()
  ├─> Executor.Execute()
  │   ├─> permissions.Service.AuthorizeTool()
  │   ├─> allowed -> Tool.Run()
  │   └─> denied -> error tool.Result without running tool
  ├─> ToolResultBlock()
  └─> repeat provider/tool steps until no tool_use or max iterations
```

This keeps tool implementation, tool execution, model-facing tool schemas, and prompt-visible tool descriptions aligned without coupling those consumers to concrete tool packages. The current session controller runs a bounded provider/tool loop within one user turn: it appends assistant `tool_use` messages and tool result messages back into the next provider request until the assistant stops requesting tools or the maximum iteration count is reached.

## CLI boundary

`runcode chat` is wired as a minimal non-TUI command. It accepts a single prompt from args or stdin, constructs the Anthropic provider, built-in tools, prompt assembler inputs, telemetry recorder, and `internal/repl.Session`, then prints the final assistant text to stdout.

The command is intentionally single-turn and shell-friendly. It does not preserve cross-turn conversation history, stream partial output to the terminal, start a Bubble Tea UI, or write transcripts. Telemetry is disabled by default; `RUNCODE_TELEMETRY=jsonl` or `--telemetry jsonl` writes structured events to stderr while stdout remains assistant text only.

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
- memory
- project context

The selected reasoning guidance is non-cacheable because it is chosen per user turn. The built-in tool descriptions are cacheable because the current built-in tool set is fixed and comes from `tools.Builtins()`. If future work makes tools dynamic through MCP, permissions, plugins, workspace configuration, or session state, the cache strategy must be reviewed. The likely options are moving tool descriptions into the dynamic section or making cache keys account for the effective tool set.

## Permission boundary

`internal/permissions` is the single authorization layer for tool execution. `internal/repl.Executor` resolves every tool call into an action, asks the permission service for a decision, records a sanitized permission telemetry event, and only then runs the tool.

Current default mode is `safe` and non-interactive:

- workspace-scoped `Read` is allowed.
- `Read` outside the configured working directory is denied.
- `Write` create and fresh-read overwrite inside the workspace are modeled as approval-requiring; without an interactive authorizer they resolve to denial.
- `Edit` exact replace inside the workspace is modeled as approval-requiring only when the target has a fresh complete read; without an interactive authorizer it resolves to denial.
- `Write`/`Edit` outside the workspace, missing read state, partial reads, stale reads, invalid targets, unknown tools, and unknown operations are denied.
- future command execution operations are modeled as approval-requiring, but without an interactive authorizer they resolve to denial.

Permission denial is returned to the model as a tool result with `is_error=true`; it does not interrupt the whole turn. This keeps tool success, tool failure, and permission denial on the same ReAct path while reserving Go errors for internal failures.

Permission telemetry records only action metadata such as operation, risk, effect, reason, resource type/count/scope, and correlation IDs. It does not record raw paths, commands, tool input, tool output, prompt text, file contents, credentials, or base URLs. Tool execution error telemetry also uses a bounded error category instead of raw error text so file paths are not emitted.

Path resolution is shared through `internal/toolpath` so tool execution and permission resolution use the same base-directory semantics. Permission containment checks resolve existing symlink targets before deciding whether a read or mutation is inside the workspace. Mutation target resolution treats existing targets and missing targets differently: existing targets check the real target path, while missing targets require an existing real parent directory inside the workspace. Overwrite/edit operations also require `tool.Context.ReadSet` to contain a complete read whose size and modification time still match the current file.

## Observability boundary

`internal/telemetry` defines the internal observability event model. Session and executor code record lifecycle events through a narrow `Recorder` interface, while concrete output sinks stay in the telemetry package.

Current events cover:

- turn lifecycle: `turn.start`, `turn.end`, `turn.error`.
- LLM request lifecycle: `llm.request.start`, `llm.request.end`, `llm.request.error`.
- tool execution lifecycle: `tool.execute.start`, `tool.execute.end`, `tool.execute.error`.
- permission decisions: `permission.decision`.

Events include correlation IDs (`trace_id`, `turn_id`, `request_id`, `tool_use_id`) and bounded metadata such as counts, durations, stop reasons, token usage, and error strings. They intentionally exclude prompts, assistant text, tool inputs, tool outputs, file contents, credentials, and base URLs.

Runtime recorders are no-op by default. JSONL mode uses a bounded async recorder so telemetry IO does not block the chat/session/tool path; a full queue drops events rather than slowing the agent.

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
| `tools` | built-in tool list, unique names, tool metadata, and current `Read`/`Write`/`Edit` registration |
| `tools/read` | file reading, line numbering, offset/limit behavior, relative paths, complete/partial `ReadSet`, errors, CRLF, cancellation, and output bounds |
| `tools/write` | file creation, fresh-read overwrite, missing/partial/stale read rejection, workspace containment, and missing parent rejection |
| `tools/edit` | exact replace, replace-all behavior, unique match enforcement, invalid input rejection, fresh-read requirement, and workspace containment |
| `internal/toolpath` | shared path resolution, workspace containment, symlink handling, mutation target resolution, and read freshness checks |
| `internal/repl` | session request construction, stream collection, permission-aware executor behavior, event channel forwarding, tool use ID propagation, and `tool_use` to `tool_result` conversion |
| `internal/permissions` | action/resource resolution, workspace containment, default safe policy, non-interactive authorization, and sanitized decision data |
| `internal/prompt` | boundary behavior, static/dynamic ordering, cache policy, environment isolation, and tool description injection |
| `internal/telemetry` | event model, JSONL output, async flush/drop behavior, memory recorder, and ID generation |
| `pkg/llm` | provider/stream interfaces and neutral content block contracts |
| `pkg/llm/providers/anthropic` | provider contract, request conversion, tool use/result mapping, stream event conversion, usage, stop reasons, and error/close behavior |
| `cmd/runcode` | `version` output, chat prompt input, config parsing, fake-runner output, and error propagation |

Recommended validation commands:

```bash
go -C "D:/我的AI/runcode" test ./...
go -C "D:/我的AI/runcode" test -race ./...
go -C "D:/我的AI/runcode" build ./cmd/runcode
```
