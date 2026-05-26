# runcode Architecture

This document tracks the architecture that is currently implemented in runcode.

## Current status

runcode is a v0.1-alpha scaffold. It has the core package boundaries needed for an AI coding companion, and the provider-neutral session controller now supports a finite ReAct loop. The CLI chat command is wired as a minimal single-turn provider-backed command; the full interactive TUI is intentionally not wired yet.

Implemented:

- `cmd/runcode`: Cobra CLI entry point with `version` and a minimal single-turn `chat` command.
- `pkg/tool`: public tool SDK boundary, including tool context, schema, events, and result types.
- `pkg/llm`: provider-neutral LLM request, message, stream, content block, cache, and tool spec types.
- `tools/registry.go`: single registration point for built-in tools.
- `tools/read`: first real built-in tool, returning line-numbered file content and updating `tool.Context.ReadSet`.
- `internal/repl`: finite ReAct session controller, tool executor, tool result conversion, and tool-spec conversion for future model tool exposure.
- `internal/telemetry`: internal observability foundation with event model, no-op recorder, bounded async recorder, memory recorder, and stderr JSONL support.
- `internal/prompt`: system prompt boundary, assembler, and static/dynamic prompt sections.
- `pkg/llm/providers/anthropic`: Anthropic SDK-backed provider skeleton with request and stream conversion tests.

Not implemented yet:

- Multi-turn interactive chat loop.
- Bubble Tea TUI.
- Permission prompts and policy engine.
- MCP, hooks, sub-agents, skills, compaction, telemetry, and persistence.
- Built-in tools beyond `Read`.

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
  ├─> Executor.Execute() -> ToolResultBlock()
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

## Observability boundary

`internal/telemetry` defines the internal observability event model. Session and executor code record lifecycle events through a narrow `Recorder` interface, while concrete output sinks stay in the telemetry package.

Current events cover:

- turn lifecycle: `turn.start`, `turn.end`, `turn.error`.
- LLM request lifecycle: `llm.request.start`, `llm.request.end`, `llm.request.error`.
- tool execution lifecycle: `tool.execute.start`, `tool.execute.end`, `tool.execute.error`.

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
| `tools` | built-in tool list, unique names, tool metadata, and current `Read` registration |
| `tools/read` | file reading, line numbering, offset/limit behavior, relative paths, `ReadSet`, errors, CRLF, cancellation, and output bounds |
| `internal/repl` | session request construction, stream collection, executor lookup, event channel forwarding, tool use ID propagation, and `tool_use` to `tool_result` conversion |
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
