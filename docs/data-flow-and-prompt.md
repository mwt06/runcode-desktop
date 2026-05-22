# runcode 执行数据流与提示词拼接结构

本文档基于当前代码实现整理，用于快速理解 `runcode` 现在的执行链路、工具数据流，以及 system prompt 的拼接方式。

当前范围仍是 provider-neutral scaffold：`internal/repl.Session` 已能运行有限多轮 ReAct loop，并可在主请求前通过 provider 预判定本轮适合的思维模型；但 `cmd/runcode chat` 还没有接入真实 LLM provider、TUI 或交互会话。

## 1. 当前执行数据流总览

```text
┌────────────────────────────────────────────────────────────────────┐
│ 用户输入 userText                                                  │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                               ▼
┌────────────────────────────────────────────────────────────────────┐
│ internal/repl.Session.RunTurn(ctx, userText)                       │
│                                                                    │
│ 初始化：                                                            │
│   messages   = [ RoleUser: userText ]                              │
│   promptOpts = s.prompt                                            │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                               ▼
┌────────────────────────────────────────────────────────────────────┐
│ 可选：AI reasoning classification                                  │
│                                                                    │
│ 条件：s.reasoning.Enabled == true                                  │
│ 请求：                                                             │
│   System   = sections.ReasoningClassifier()                        │
│   Messages = [ RoleUser: userText ]                                │
│   Tools    = nil                                                   │
│                                                                    │
│ provider 返回 JSON：                                                │
│   {"scenario":"architecture","confidence":"medium"}           │
│                                                                    │
│ 解析后：                                                            │
│   promptOpts.Reasoning =                                           │
│     sections.ReasoningGuidance(classification.Scenario)            │
│                                                                    │
│ 注意：分类请求不计入 MaxIterations，                                │
│      不进入 TurnResult.Requests，                                  │
│      不进入主 conversation messages。                              │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                               ▼
┌────────────────────────────────────────────────────────────────────┐
│ for iteration < maxIterations                                      │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                               ▼
┌────────────────────────────────────────────────────────────────────┐
│ buildRequestWithMessagesAndPrompt(messages, promptOpts)            │
│                                                                    │
│ 1. promptOpts.Tools = s.tools                                      │
│ 2. prompt.BuildSystemPrompt(promptOpts)                            │
│ 3. ToolSpecs(s.tools)                                              │
│ 4. cloneMessages(messages)                                         │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                               ▼
┌────────────────────────────────────────────────────────────────────┐
│ llm.Request                                                        │
│                                                                    │
│ Model       = s.model                                              │
│ System      = []llm.ContentBlock                                   │
│ Messages    = cloned conversation messages                         │
│ Tools       = []llm.ToolSpec                                       │
│ MaxTokens   = s.maxTokens                                          │
│ Temperature = s.temperature                                        │
│ Metadata    = s.metadata                                           │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                               ▼
┌────────────────────────────────────────────────────────────────────┐
│ s.provider.Stream(ctx, req)                                        │
│                                                                    │
│ 返回 llm.Stream：                                                   │
│   Events() <-chan llm.StreamEvent                                  │
│   Err() error                                                      │
│   Close() error                                                    │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                               ▼
┌────────────────────────────────────────────────────────────────────┐
│ collectAssistantMessage(ctx, stream)                               │
│                                                                    │
│ 消费 StreamEvent：                                                  │
│   message_start         -> 忽略                                    │
│   content_block_start   -> 建立 blockAccumulator                   │
│   content_block_delta   -> 累积 Text / Thinking / Signature / JSON │
│   content_block_stop    -> materialize block                       │
│   message_stop          -> 返回 RoleAssistant message              │
│                                                                    │
│ streamAssistant defer stream.Close()                               │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                               ▼
┌────────────────────────────────────────────────────────────────────┐
│ assistant message                                                  │
│                                                                    │
│ 如果没有 tool_use：                                                 │
│   返回 TurnResult                                                  │
│                                                                    │
│ 如果包含 tool_use：                                                 │
│   检查是否达到 MaxIterations                                       │
│   未达到则进入工具执行                                             │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                               ▼
┌────────────────────────────────────────────────────────────────────┐
│ executeToolUses(ctx, assistant)                                    │
│                                                                    │
│ 遍历 assistant.Content：                                            │
│   只处理 Type == tool_use 的 block                                 │
│                                                                    │
│ 对每个 tool_use 构造 ExecuteRequest：                               │
│   Name      = block.Name                                           │
│   Input     = block.Input                                          │
│   ToolUseID = block.ID                                             │
│   Context   = s.toolContext                                        │
│   Events    = s.toolEvents                                         │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                               ▼
┌────────────────────────────────────────────────────────────────────┐
│ Executor.Execute(ctx, ExecuteRequest)                              │
│                                                                    │
│ 1. 校验工具名                                                       │
│ 2. 从 map[string]tool.Tool 查找 runner                             │
│ 3. 设置 tool.Context.ToolUseID                                     │
│ 4. runner.Run(ctx, input, tctx, events)                            │
│ 5. 返回 ExecuteResult                                              │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                               ▼
┌────────────────────────────────────────────────────────────────────┐
│ ToolResultBlock(ExecuteResult)                                     │
│                                                                    │
│ tool.Result                                                        │
│   └─ []tool.ResultContent                                          │
│                                                                    │
│ 转换为：                                                            │
│   llm.ContentBlock{                                                │
│     Type:      tool_result,                                        │
│     ToolUseID: original tool_use ID,                               │
│     Content:   []llm.ContentBlock{text/json-as-text},              │
│   }                                                               │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                               ▼
┌────────────────────────────────────────────────────────────────────┐
│ RoleTool message                                                   │
│                                                                    │
│ llm.Message{                                                       │
│   Role:    llm.RoleTool,                                           │
│   Content: []tool_result blocks,                                   │
│ }                                                                  │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                               ▼
┌────────────────────────────────────────────────────────────────────┐
│ append messages                                                    │
│                                                                    │
│ messages = append(messages, assistant)                             │
│ messages = append(messages, toolMessage)                           │
│                                                                    │
│ 下一轮 provider request 会携带：                                    │
│   user -> assistant(tool_use) -> tool(tool_result) -> ...          │
└──────────────────────────────┬─────────────────────────────────────┘
                               │
                               └─────────────── 回到下一次 iteration
```

## 2. 当前工具注册与工具执行数据流

```text
┌──────────────────────────────┐
│ tools.Builtins()             │
└──────────────┬───────────────┘
               │
               ▼
┌──────────────────────────────┐
│ []tool.Tool                  │
│                              │
│ 当前只有：                    │
│   read.New() -> Read         │
└───────┬──────────────┬───────┘
        │              │
        │              │
        ▼              ▼
┌──────────────────┐  ┌──────────────────────────────┐
│ NewExecutor()    │  │ ToolSpecs()                   │
│                  │  │                              │
│ 建立执行索引：    │  │ 暴露给 provider 的工具 schema │
│ map[name]Tool    │  │ []llm.ToolSpec                │
└────────┬─────────┘  └──────────────┬───────────────┘
         │                           │
         ▼                           ▼
┌──────────────────┐  ┌──────────────────────────────┐
│ Executor.Execute │  │ llm.Request.Tools            │
└────────┬─────────┘  └──────────────────────────────┘
         │
         ▼
┌────────────────────────────────────────────────────┐
│ Read.Run(ctx, rawInput, tool.Context, events)      │
│                                                    │
│ 输入 JSON：                                        │
│   {                                                │
│     "path": "sample.txt",                       │
│     "offset": 0,                                 │
│     "limit": 2000                                │
│   }                                                │
│                                                    │
│ 输出 tool.Result：                                 │
│   Content[0].Type = text                           │
│   Content[0].Text = "1\t...\n2\t..."             │
│                                                    │
│ 副作用：                                           │
│   tool.Context.ReadSet[absPath] = ReadFile metadata│
└────────────────────────────────────────────────────┘
```

## 3. 多轮 ReAct message 形态

### 3.1 无工具调用

```text
可选 classification request:
┌──────────────┐
│ user         │  "解释这个文件"
└──────┬───────┘
       ▼
Provider returns:
┌──────────────┐
│ assistant    │  {"scenario":"general","confidence":"medium"}
└──────┬───────┘
       ▼
主 Request #1 messages:
┌──────────────┐
│ user         │  "解释这个文件"
└──────┬───────┘
       ▼
Provider returns:
┌──────────────┐
│ assistant    │  text: "..."
└──────────────┘

RunTurn 返回。
```

### 3.2 一次工具调用

```text
classification request 先选择本轮 reasoning guidance
       │
       ▼
主 Request #1 messages:
┌──────────────┐
│ user         │  "读取 sample.txt"
└──────┬───────┘
       ▼
Provider returns:
┌──────────────┐
│ assistant    │  tool_use: Read({"path":"sample.txt"})
└──────┬───────┘
       ▼
Executor executes Read
       ▼
┌──────────────┐
│ tool         │  tool_result for toolu_xxx
└──────┬───────┘
       ▼
主 Request #2 messages:
┌──────────────┐
│ user         │
├──────────────┤
│ assistant    │  tool_use
├──────────────┤
│ tool         │  tool_result
└──────┬───────┘
       ▼
Provider returns:
┌──────────────┐
│ assistant    │  final text
└──────────────┘
```

`MaxIterations` 限制主 ReAct provider 调用次数，默认值是 `8`。Reasoning classification request 不计入该次数。如果最后一次主 provider 请求仍返回 `tool_use`，`RunTurn` 返回 `ErrMaxIterations`，并且不会执行这一轮工具，避免产生无法回传给 provider 的悬空 tool result 或额外副作用。

## 4. system prompt 拼接结构

当前入口：`internal/prompt.BuildSystemPrompt(opts AssemblerOpts)`。

### 4.1 输入结构

```go
type AssemblerOpts struct {
    CWD        string
    Date       string
    Tools      []tool.Tool
    ProjectCtx string
    Memory     string
    ShellInfo  string
    Reasoning  string
}
```

### 4.2 拼接顺序线框图

```text
BuildSystemPrompt(opts)
│
├─ static sections，CacheControlEphemeral
│
│  ┌──────────────────────────────────────────────┐
│  │ 1. sections.Intro()                          │
│  ├──────────────────────────────────────────────┤
│  │ 2. sections.System()                         │
│  ├──────────────────────────────────────────────┤
│  │ 3. sections.UsingTools(opts.Tools)           │
│  ├──────────────────────────────────────────────┤
│  │ 4. sections.Actions()                        │
│  ├──────────────────────────────────────────────┤
│  │ 5. sections.ToneAndStyle()                   │
│  └──────────────────────────────────────────────┘
│
├─ boundary block，CacheControlEphemeral
│
│  ┌──────────────────────────────────────────────┐
│  │ __RUNCODE_DYNAMIC_BOUNDARY__                 │
│  └──────────────────────────────────────────────┘
│
└─ dynamic sections，CacheControlNone

   ┌──────────────────────────────────────────────┐
   │ 6. opts.Reasoning                            │
   ├──────────────────────────────────────────────┤
   │ 7. sections.EnvInfo(CWD, Date, ShellInfo)    │
   ├──────────────────────────────────────────────┤
   │ 8. opts.Memory                               │
   ├──────────────────────────────────────────────┤
   │ 9. opts.ProjectCtx                           │
   └──────────────────────────────────────────────┘
```

### 4.3 输出结构

输出不是单个字符串，而是多个 `llm.ContentBlock`：

```text
[]llm.ContentBlock{
  {Type: text, Text: Intro,          Cache: ephemeral},
  {Type: text, Text: System,         Cache: ephemeral},
  {Type: text, Text: UsingTools,     Cache: ephemeral},
  {Type: text, Text: Actions,        Cache: ephemeral},
  {Type: text, Text: ToneAndStyle,   Cache: ephemeral},
  {Type: text, Text: Boundary,       Cache: ephemeral},
  {Type: text, Text: Reasoning,      Cache: none},
  {Type: text, Text: EnvInfo,        Cache: none},
  {Type: text, Text: Memory,         Cache: none},
  {Type: text, Text: ProjectCtx,     Cache: none},
}
```

空字符串 section 会被跳过。

## 5. 当前原始提示词文本

以下内容来自 `internal/prompt/sections` 当前实现。

### 5.1 Intro

```text
You are an AI coding companion that helps users with programming tasks.
```

### 5.2 System

```text
You are a capable, terminal-native coding agent that reads, writes, edits, and reasons about code.
You run tools, receive results, and iterate on a ReAct loop to complete the user's task.
Always prioritize correctness, security, and the user's instructions.
```

### 5.3 UsingTools 模板

当工具列表为空时，该 section 返回空字符串。

当存在工具时，开头固定为：

```text
You have the following tools available:
```

随后每个工具追加：

```text

Tool: <tool.Name()>
Description: <tool.Description()>
```

当前 `tools.Builtins()` 只有 `Read`，因此实际文本为：

```text
You have the following tools available:

Tool: Read
Description: Read a text file and return line-numbered content.
```

注意：当前 prompt 中只列出工具名称和描述；完整 JSON schema 是通过 `llm.Request.Tools` 传给 provider，而不是拼在 prompt 文本里。

### 5.4 Actions

```text
When given a task:
1. Analyze what the user is asking for
2. Use available tools to gather context
3. Plan and execute the needed changes step by step
4. Verify results before completing
```

### 5.5 ToneAndStyle

```text
Response guidelines:
- Be concise and direct
- Use bullet points for lists
- Explain reasoning when decisions are non-obvious
- Show diffs or outputs instead of vague descriptions
```

### 5.6 ReasoningClassifier

分类请求使用独立 system prompt，不带 tools，不进入主 conversation messages：

```text
Classify the user's task into exactly one reasoning scenario.
Return only compact JSON with this shape: {"scenario":"<scenario>","confidence":"low|medium|high"}.
Allowed scenarios:
- troubleshooting: debugging, failure analysis, regressions, flaky behavior, broken tests, unexpected output
- proposal: writing implementation plans, comparing approaches, product or technical proposals
- architecture: system design, boundaries, data flow, abstractions, long-term structure
- project_management: sequencing work, prioritization, delivery tracking, coordination
- incident_response: urgent mitigation, production incidents, time-sensitive recovery
- general: simple tasks or tasks that do not clearly match another scenario
```

### 5.7 ReasoningGuidance 模板

主请求使用受控模板，不把 AI 分类输出的自由文本原样注入。示例：

```text
Selected reasoning mode: architecture
Recommended reasoning model: first principles + systems thinking + inversion

Use this checklist to guide the turn when it helps:
1. What is the problem?
2. What is the goal?
3. What facts are known?
4. What assumptions exist?
5. How can the assumptions be verified?
6. What options are available?
7. What are each option's costs, benefits, and risks?
8. Which option is recommended?
9. How should it be executed?
10. How will the result be verified?

Keep simple tasks concise. Do not force every response into all ten steps; surface only the analysis that materially helps the user.
```

场景与模型映射：

```text
troubleshooting   -> 5 Whys + hypothesis validation + Occam's razor
proposal          -> Pyramid principle + MECE + cost-benefit analysis
architecture      -> first principles + systems thinking + inversion
project_management-> closed-loop thinking + 80/20 rule
incident_response -> OODA + hypothesis validation
general           -> the general analysis checklist
```

### 5.8 DynamicBoundary

```text
__RUNCODE_DYNAMIC_BOUNDARY__
```

### 5.9 EnvInfo 模板

`EnvInfo` 按非空字段拼接，每项一行：

```text
Current working directory: <CWD>
Current date: <Date>
Shell: <ShellInfo>
```

如果某个字段为空，则跳过对应行。

### 5.10 Memory

`opts.Memory` 由调用方传入。当前 prompt assembler 不解析、不改写，只作为 dynamic section 追加。

```text
<opts.Memory>
```

### 5.11 ProjectCtx

`opts.ProjectCtx` 由调用方传入。当前 prompt assembler 不解析、不改写，只作为 dynamic section 追加。

```text
<opts.ProjectCtx>
```

## 6. 示例：当前默认工具集下的 system prompt blocks

假设：

```text
CWD       = D:/我的AI/runcode
Date      = 2026-05-21
ShellInfo = bash on Windows
Reasoning = sections.ReasoningGuidance("architecture")
Memory    = <empty>
ProjectCtx= <empty>
Tools     = tools.Builtins() // Read
```

则 provider request 中的 `System` 大致为：

```text
[block 1 | cache: ephemeral]
You are an AI coding companion that helps users with programming tasks.

[block 2 | cache: ephemeral]
You are a capable, terminal-native coding agent that reads, writes, edits, and reasons about code.
You run tools, receive results, and iterate on a ReAct loop to complete the user's task.
Always prioritize correctness, security, and the user's instructions.

[block 3 | cache: ephemeral]
You have the following tools available:

Tool: Read
Description: Read a text file and return line-numbered content.

[block 4 | cache: ephemeral]
When given a task:
1. Analyze what the user is asking for
2. Use available tools to gather context
3. Plan and execute the needed changes step by step
4. Verify results before completing

[block 5 | cache: ephemeral]
Response guidelines:
- Be concise and direct
- Use bullet points for lists
- Explain reasoning when decisions are non-obvious
- Show diffs or outputs instead of vague descriptions

[block 6 | cache: ephemeral]
__RUNCODE_DYNAMIC_BOUNDARY__

[block 7 | cache: none]
Selected reasoning mode: architecture
Recommended reasoning model: first principles + systems thinking + inversion
...

[block 8 | cache: none]
Current working directory: D:/我的AI/runcode
Current date: 2026-05-21
Shell: bash on Windows
```

## 7. 关键边界说明

### 7.1 prompt 文本与 tool schema 分离

```text
Prompt text:
  sections.UsingTools(opts.Tools)
  -> 给模型读的人类可理解工具说明

Tool schema:
  repl.ToolSpecs(s.tools)
  -> 给 provider 的结构化 tool definitions
```

两者都来自同一个 `s.tools` 快照，避免 prompt 可见工具与实际可执行工具分叉。

### 7.2 reasoning 分类与主请求分离

```text
classification request:
  System   = sections.ReasoningClassifier()
  Messages = [userText]
  Tools    = nil
  Result   = JSON scenario

main request:
  System   = BuildSystemPrompt(promptOpts with ReasoningGuidance)
  Messages = ReAct conversation messages
  Tools    = ToolSpecs(s.tools)
```

分类请求失败时默认 fallback 到配置的默认场景；`Strict` 模式下返回错误并不发送主请求。

### 7.3 static 与 dynamic cache 边界

```text
static / cacheable:
  Intro
  System
  UsingTools
  Actions
  ToneAndStyle
  DynamicBoundary

dynamic / non-cacheable:
  Reasoning
  EnvInfo
  Memory
  ProjectCtx
```

`Reasoning` 是本轮 AI classification 的结果，属于 per-turn dynamic prompt，因此不可缓存。当前内置工具集固定，因此 `UsingTools` 被放在 static/cacheable 区域。若未来工具集会被 MCP、插件、权限、workspace 配置或 session state 动态改变，需要重新评估缓存边界。

### 7.4 CLI 尚未接入 Session

```text
cmd/runcode chat
  └─ 当前只打印 banner 和 not implemented 消息

internal/repl.Session
  └─ 已有 provider-neutral ReAct loop 和 reasoning 预判定能力，
     但还没有被 chat 命令调用
```

也就是说，当前这些数据流主要由测试验证，并不是用户运行 `runcode chat` 时实际触发的交互链路。
