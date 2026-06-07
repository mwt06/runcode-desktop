# Session Handoff — runcode 脚手架初始化

> 日期：2026-05-07
> 项目：`runcode`（中文名：奔跑的代码）
> 项目位置：`d:/我的AI/runcode/`
> 参考源码：`d:/我的AI/claude-code-cli-master/`

## 本轮目标

用户最初希望基于 Claude Code CLI 架构，用 Go 构建自己的智能体项目「奔跑的代码」。经过计划模式确认后，本轮实际执行范围被用户修正为：

> 只做第一个选项：**脚手架骨架**。

也就是先创建一个开源级 Go 项目骨架，确保能构建、能运行基础命令，后续再继续实现 `pkg/tool`、`pkg/llm`、工具系统和 TUI。

## 已确认的长期项目决策

- 项目名：`runcode`
- 中文名：奔跑的代码
- 二进制名：`runcode`
- 仓库名：`runcode`
- Go module：`github.com/wt68/runcode`
  - `wt68` 是当前确认的 GitHub owner。
- 项目位置：`d:/我的AI/runcode/`
- 上下文文件命名：优先 `RUNCODE.md`，兼容读取 `CLAUDE.md`
- 技术栈：
  - Go 1.26+
  - Cobra
  - Bubble Tea + Bubbles + Lip Gloss（后续）
  - anthropic-sdk-go + openai-go（后续）
  - Viper（后续）
  - modernc.org/sqlite（后续）
  - GoReleaser
- v1.0 长期目标：全功能 Go 版 AI 编程伴侣，包括 MCP、Sub-agent、Coordinator、Skill、多 Provider、上下文压缩。
- v0.1 MVP 原计划：TUI + Anthropic + 7 工具（Read/Write/Edit/Glob/Grep/Bash/TodoWrite）+ 默认权限 + 系统提示静态/动态边界 + SQLite transcript。

## 计划文件位置

详细架构计划已在 Claude Code 计划模式中保存：

`C:\Users\wtgx1\.claude\plans\dapper-frolicking-wolf.md`

如果清理会话后需要恢复完整设计，可先读取该文件。

## 本轮已完成内容

### 1. 创建项目目录

已创建：

`d:/我的AI/runcode/`

### 2. Go module

已创建：

- `go.mod`
- `go.sum`

当前 `go.mod`：

```go
module github.com/wt68/runcode

go 1.26

require github.com/spf13/cobra v1.10.2

require (
    github.com/inconshreveable/mousetrap v1.1.0 // indirect
    github.com/spf13/pflag v1.0.9 // indirect
)
```

### 3. CLI 入口

已创建：

`cmd/runcode/main.go`

当前能力：

- `runcode version`
- `runcode --help`
- `runcode chat`

`chat` 当前只是占位，打印 ASCII banner 和提示：

```text
chat is not implemented yet — coming in v0.1 milestone
```

这符合本轮「脚手架骨架」范围。

### 4. 开源治理文件

已创建：

- `README.md`
- `README.zh-CN.md`
- `LICENSE`
- `NOTICE`
- `CONTRIBUTING.md`
- `CODE_OF_CONDUCT.md`
- `SECURITY.md`
- `CHANGELOG.md`

说明：

- `LICENSE` 使用 Apache-2.0。
- `README` 中 GitHub URL 已更新为 `github.com/wt68/runcode`。
- `SECURITY.md` 和 `CODE_OF_CONDUCT.md` 中暂时使用占位联系信息，后续开源发布前要替换。

### 5. GitHub 配置

已创建：

- `.github/workflows/ci.yml`
- `.github/workflows/release.yml`
- `.github/workflows/codeql.yml`
- `.github/ISSUE_TEMPLATE/bug.yml`
- `.github/ISSUE_TEMPLATE/feature.yml`
- `.github/PULL_REQUEST_TEMPLATE.md`
- `.github/dependabot.yml`

CI 设计：

- `lint`：ubuntu + golangci-lint
- `test`：linux/macos/windows matrix + `go test -race ./...`
- `build`：`go build ./cmd/runcode` + GoReleaser snapshot build

注意：仓库没发布前，workflow 不影响本地构建。

### 6. 工程配置

已创建：

- `.gitignore`
- `.golangci.yml`
- `.goreleaser.yaml`
- `Makefile`

Makefile 当前目标：

```make
make build
make run
make test
make lint
make fmt
make tidy
make snapshot
make clean
```

GoReleaser 当前配置：

- project_name: `runcode`
- main: `./cmd/runcode`
- binary: `runcode`
- targets:
  - linux amd64/arm64
  - darwin amd64/arm64
  - windows amd64/arm64
- release owner 为 `wt68`

### 7. 空目录骨架

已创建并用 `.gitkeep` 占位：

```text
internal/app/components/
internal/repl/
internal/permissions/
internal/persistence/sqlite/
internal/persistence/claudemd/
internal/persistence/settings/
internal/persistence/transcript/
internal/persistence/migrate/
internal/prompt/sections/
internal/hooks/
internal/mcp/
internal/coordinator/
internal/compaction/
internal/cost/
internal/session/
internal/telemetry/
internal/ui/
pkg/tool/
pkg/llm/providers/anthropic/
pkg/llm/providers/openai/
pkg/agent/
pkg/skill/
pkg/command/
pkg/plugin/
tools/read/
tools/write/
tools/edit/
tools/glob/
tools/grep/
tools/bash/
tools/todo/
prompts/templates/
prompts/agents/
prompts/skills/
examples/custom-tool/
```

### 8. 架构占位文档

已创建：

`docs/architecture.md`

内容是简版架构占位，后续需要把正式设计逐步迁入。

### 9. 本 handoff 文档

已创建：

`docs/session-handoff.md`

清理当前 Claude 会话后，可先读取它恢复上下文。

## 已验证命令

已成功运行：

```bash
cd "d:/我的AI/runcode"
go test ./...
go build ./cmd/runcode
./runcode.exe version
./runcode.exe --help
./runcode.exe chat
```

实际验证输出要点：

```text
?    github.com/wt68/runcode/cmd/runcode [no test files]

runcode 0.1.0-alpha
  commit: unknown
  built:  unknown
  go:     windows/amd64 go1.26.2
```

`./runcode.exe chat` 会显示 ASCII banner 和占位提示。

最后又执行过：

```bash
gofmt -w "cmd/runcode/main.go"
go test ./...
go build ./cmd/runcode
```

通过。

## Go 代理配置

用户提醒：`GOPROXY=https://goproxy.cn 一会配置一下`

已执行并验证：

```bash
go env -w GOPROXY=https://goproxy.cn,direct
go env GOPROXY
```

当前输出：

```text
https://goproxy.cn,direct
```

## 重要注意事项

1. 当前工作目录 `d:/我的AI/claude-code-cli-master/` 不是新项目目录。
2. 新项目目录是 `d:/我的AI/runcode/`。
3. 当前 `d:/我的AI/runcode/` 还不是 git 仓库（本轮未执行 `git init`）。如果后续要版本管理，需要显式执行：

   ```bash
   cd "d:/我的AI/runcode"
   git init
   ```

4. 当前 `chat` 命令只是占位，尚未接 Bubble Tea、LLM 或工具系统。
5. GitHub owner 已确认为 `wt68`，全仓 module/import/文档 URL 已更新。
6. `SECURITY.md` 和 `CODE_OF_CONDUCT.md` 中的联系邮箱也还是占位。
7. 本轮没有提交 git commit。
8. 本轮没有实现 `pkg/tool`、`pkg/llm` 或任何真实工具。

## 下一步推荐

清理会话后，建议从这里继续：

### 下一轮目标：实现核心接口骨架

建议任务范围：

1. 创建 `pkg/tool`：
   - `tool.go`
   - `context.go`
   - `result.go`
   - `schema.go`
2. 创建 `pkg/llm`：
   - `provider.go`
   - `message.go`
   - `stream.go`
3. 写最小单元测试，确保 public API 编译。
4. 暂不接真实 provider，先让架构成型。

### 下一轮之后

再继续：

1. 实现 `tools/read` 真实 Read 工具。
2. 实现 `tools/registry.go`。
3. 实现 `internal/prompt/boundary.go` 和基础 prompt assembler。
4. 实现最小 REPL controller skeleton。
5. 再接 Bubble Tea TUI。

## 继续会话时建议给 Claude 的第一句话

可以直接说：

> 读取 `d:/我的AI/runcode/docs/session-handoff.md` 和 `C:\Users\wtgx1\.claude\plans\dapper-frolicking-wolf.md`，继续 runcode 项目。下一步实现 `pkg/tool` 和 `pkg/llm` 核心接口骨架，不要扩展到真实 LLM 或 TUI。

## 当前文件清单（核心）

```text
runcode/
├── .github/
│   ├── ISSUE_TEMPLATE/
│   │   ├── bug.yml
│   │   └── feature.yml
│   ├── workflows/
│   │   ├── ci.yml
│   │   ├── codeql.yml
│   │   └── release.yml
│   ├── PULL_REQUEST_TEMPLATE.md
│   └── dependabot.yml
├── cmd/runcode/main.go
├── docs/architecture.md
├── docs/session-handoff.md
├── .gitignore
├── .golangci.yml
├── .goreleaser.yaml
├── CHANGELOG.md
├── CODE_OF_CONDUCT.md
├── CONTRIBUTING.md
├── go.mod
├── go.sum
├── LICENSE
├── Makefile
├── NOTICE
├── README.md
├── README.zh-CN.md
└── SECURITY.md
```

## 2026-05-07 续作：核心接口骨架已完成

本节记录清理会话前的最新进展。若与上文较早的“下一步推荐”冲突，以本节为准。

### 本轮目标

用户要求继续 `runcode` 项目，下一步实现：

- `pkg/tool` 核心接口骨架
- `pkg/llm` 核心接口骨架
- 不扩展到真实 LLM provider
- 不扩展到 TUI

### 本轮已完成内容

#### 1. 新增 `pkg/tool` public API 骨架

已创建：

```text
pkg/tool/tool.go
pkg/tool/context.go
pkg/tool/result.go
pkg/tool/schema.go
pkg/tool/tool_test.go
```

当前设计要点：

- `Tool` interface：
  - `Name() string`
  - `Description() string`
  - `InputSchema() Schema`
  - `IsConcurrencySafe() bool`
  - `Run(ctx context.Context, input json.RawMessage, tctx *Context, out chan<- Event) (Result, error)`
- `Context`：承载工具执行元数据，如工作目录、session/message/tool use ID、read set、env、metadata。
- `Schema`：轻量 JSON Schema 结构，支持 object/array/string/number/integer/boolean、properties、required、items、enum、default、additionalProperties。
- `Result`：工具最终结构化结果，当前支持 text/json content block。
- `Event`：工具运行期间的流式事件，当前类型包括 started/progress/output/completed。

#### 2. 新增 `pkg/llm` public API 骨架

已创建：

```text
pkg/llm/provider.go
pkg/llm/message.go
pkg/llm/stream.go
pkg/llm/provider_test.go
```

当前设计要点：

- `Provider` interface：
  - `Name() string`
  - `Capabilities() Capabilities`
  - `Stream(ctx context.Context, req Request) (Stream, error)`
- `Capabilities`：记录 provider 是否支持 cache control、thinking，以及最大上下文 token。
- `Request`：中性请求结构，包含 model、messages、system blocks、tools、max tokens、temperature、metadata。
- `Message` / `ContentBlock`：中性消息结构，支持 text/tool_use/tool_result/thinking/image。
- `Stream` interface：
  - `Events() <-chan StreamEvent`
  - `Err() error`
  - `Close() error`
- `StreamEvent`：中性流式事件，支持 message/content block start/delta/stop、usage、stop reason、provider data。

#### 3. 新增最小契约测试

已创建外部包视角测试：

```text
pkg/tool/tool_test.go
pkg/llm/provider_test.go
```

目的：确保 public API 可被外部实现方使用，并在后续重构时提供最小编译契约保护。

### 已验证命令

在 `D:/我的AI/runcode` 下已成功运行：

```bash
gofmt -w "pkg/tool/tool.go" "pkg/tool/context.go" "pkg/tool/result.go" "pkg/tool/schema.go" "pkg/tool/tool_test.go" "pkg/llm/provider.go" "pkg/llm/message.go" "pkg/llm/stream.go" "pkg/llm/provider_test.go"
go test ./...
go build ./cmd/runcode
```

验证输出要点：

```text
?    github.com/wt68/runcode/cmd/runcode [no test files]
ok   github.com/wt68/runcode/pkg/llm
ok   github.com/wt68/runcode/pkg/tool
```

### 当前注意事项

1. `D:/我的AI/runcode` 当前仍不是 git 仓库；执行 `git status --short` 会返回：

   ```text
   fatal: not a git repository (or any of the parent directories): .git
   ```

2. 本轮没有执行 `git init`、没有提交 commit。
3. 本轮没有接入 `anthropic-sdk-go` 或 `openai-go`。
4. 本轮没有创建真实 provider adapter。
5. 本轮没有实现任何真实内置工具。
6. 本轮没有改动 `cmd/runcode/main.go` 的 chat 占位行为。
7. `go.mod` 仍是：

   ```go
   module github.com/wt68/runcode
   go 1.26
   ```

8. `GOPROXY` 之前已配置为：

   ```text
   https://goproxy.cn,direct
   ```

### 下一步推荐

下一轮建议只做一个小范围：实现第一个真实工具和工具注册骨架。

建议任务范围：

1. 创建 `tools/registry.go`：
   - 显式注册内置工具。
   - 返回 `[]tool.Tool` 或 `map[string]tool.Tool`，按后续 executor 易用性决定。
2. 实现 `tools/read`：
   - 读取文本文件。
   - 支持 `path`、`offset`、`limit`。
   - 默认读取前 2000 行。
   - 返回带行号文本。
   - 更新 `tool.Context.ReadSet`，为后续 Write/Edit 的“写前必须读”约定铺路。
3. 添加 `tools/read` 单元测试：
   - 使用 `t.TempDir()`。
   - 覆盖正常读取、offset/limit、文件不存在、目录输入等基础场景。
4. 运行：

   ```bash
   gofmt -w ...
   go test ./...
   go build ./cmd/runcode
   ```

暂不做：

- Write/Edit/Grep/Glob/Bash/TodoWrite
- LLM provider
- ReAct loop
- Bubble Tea TUI
- SQLite transcript
- 权限弹窗

### 清理会话后建议给 Claude 的第一句话

```text
读取 d:/我的AI/runcode/docs/session-handoff.md 和 C:\Users\wtgx1\.claude\plans\dapper-frolicking-wolf.md，继续 runcode 项目。注意 pkg/tool 和 pkg/llm 核心接口骨架已经完成并通过 go test/go build。下一步只实现 tools/registry.go 和 tools/read 真实 Read 工具，不要扩展到其他工具、真实 LLM 或 TUI。
```

### 当前状态一句话总结

`runcode` 现在已经具备可构建 CLI 脚手架、开源项目元数据、目录骨架，以及 `pkg/tool` / `pkg/llm` 两个 public SDK 边界的最小接口与契约测试；下一步应从 `tools/read` 开始接入第一个真实工具。

## 2026-05-07 续作：Read 工具与工具注册已完成

本节记录清理会话前的最新进展。若与上文较早的“下一步推荐”冲突，以本节为准。

### 本轮目标

用户要求继续 `runcode` 项目，且严格限定范围：

- 只实现 `tools/registry.go`
- 只实现 `tools/read` 真实 Read 工具
- 不扩展到其他工具
- 不接入真实 LLM
- 不实现 executor / ReAct loop
- 不修改 TUI 或 `chat` 占位行为

### 本轮已完成内容

#### 1. 新增真实 Read 工具

已创建：

```text
tools/read/read.go
```

当前设计要点：

- `read.Tool` 实现 `pkg/tool.Tool` 接口。
- `read.New()` 返回 `tool.Tool`。
- `Name()` 返回 `Read`。
- `Description()` 描述为读取文本文件并返回带行号内容。
- `InputSchema()` 支持：
  - `path`：必填 string。
  - `offset`：可选 integer，0-based 行偏移，默认 0。
  - `limit`：可选 integer，最大读取行数，`<= 0` 时默认 2000。
- `IsConcurrencySafe()` 返回 `true`。
- `Run()` 行为：
  - 解析 JSON 输入。
  - 校验 `path` 必填。
  - 校验 `offset >= 0`。
  - 相对路径按 `tool.Context.WorkingDirectory` 解析；若为空则使用当前进程工作目录。
  - 使用绝对清理路径读取文件。
  - 目录输入返回错误。
  - 返回文本格式为：`行号<TAB>内容`。
  - 文件末尾换行不会额外生成空输出行。
  - 支持 `context.Context` 取消。
  - 成功读取后更新 `tool.Context.ReadSet`，key 为解析后的绝对路径，value 包含 `Path`、`Size`、`ModTime`。

#### 2. 新增工具注册骨架

已创建：

```text
tools/registry.go
```

当前设计要点：

- package 为 `tools`。
- 显式导入 `github.com/wt68/runcode/tools/read`。
- 提供：

```go
func Builtins() []tool.Tool
```

- 当前只返回 `read.New()`。
- 暂未注册 Write/Edit/Glob/Grep/Bash/TodoWrite，即使这些目录存在 `.gitkeep`。

#### 3. 新增测试

已创建：

```text
tools/read/read_test.go
tools/registry_test.go
```

测试覆盖：

- Read 正常读取并返回带行号文本。
- `offset` / `limit` 行为。
- 相对路径按 `WorkingDirectory` 解析。
- `limit <= 0` 使用默认 2000 行。
- 成功读取后更新 `ReadSet`。
- 文件不存在返回错误。
- 目录输入返回错误。
- 缺少 `path` 返回错误。
- `tools.Builtins()` 当前只包含 `Read`。
- 内置工具名不重复。

### 已验证命令

在任意当前目录下使用 `go -C` 指向项目目录，已成功运行：

```bash
go -C "D:/我的AI/runcode" test ./...
go -C "D:/我的AI/runcode" build ./cmd/runcode
```

测试输出要点：

```text
?    github.com/wt68/runcode/cmd/runcode [no test files]
ok   github.com/wt68/runcode/pkg/llm
ok   github.com/wt68/runcode/pkg/tool
ok   github.com/wt68/runcode/tools
ok   github.com/wt68/runcode/tools/read
```

构建通过，无输出。

### 当前注意事项

1. `D:/我的AI/runcode` 仍不是 git 仓库，本轮没有执行 `git init`、没有提交 commit。
2. 本轮没有新增第三方依赖，`go.mod` 未因本轮 Read 工具改变。
3. 本轮没有修改 `cmd/runcode/main.go`，`runcode chat` 仍是占位。
4. 本轮没有实现 executor，因此 Read 工具目前已有实现和注册函数，但尚未被 CLI 调用。
5. 本轮没有实现其他工具，也没有接入真实 LLM provider。
6. 之前曾误在 `D:/我的AI/claude-code-cli-master` 默认目录运行 `go test ./...` / `go build ./cmd/runcode`，失败原因只是目录不对；随后使用 `go -C "D:/我的AI/runcode" ...` 验证通过。

### 下一步推荐

下一轮建议仍保持小范围，不要直接跳到 TUI。推荐二选一：

1. 实现最小工具 executor skeleton：
   - 输入工具名 + JSON 参数。
   - 从 `tools.Builtins()` 查找工具。
   - 调用 `tool.Tool.Run()`。
   - 测试只覆盖 Read 工具执行链路。
2. 或实现 `internal/prompt/boundary.go` 与最小 prompt assembler skeleton：
   - 建立静态/动态 prompt 边界。
   - 不接真实 LLM。

如果继续工具链，建议优先做 executor skeleton，因为它能把 `tools/registry.go` 和 `tools/read` 串起来，但仍不需要真实 LLM 或 TUI。

### 清理会话后建议给 Claude 的第一句话

```text
读取 d:/我的AI/runcode/docs/session-handoff.md 和 C:\Users\wtgx1\.claude\plans\dapper-frolicking-wolf.md，继续 runcode 项目。注意 pkg/tool、pkg/llm 核心接口骨架已经完成；tools/registry.go 和 tools/read 真实 Read 工具也已经完成并通过 go test/go build。下一步不要直接做真实 LLM 或 TUI，先按 handoff 的下一步推荐继续最小 executor skeleton 或 prompt boundary skeleton。
```

### 当前状态一句话总结

`runcode` 现在已经具备可构建 CLI 脚手架、`pkg/tool` / `pkg/llm` public API 骨架、真实 `Read` 工具、内置工具注册函数，以及覆盖这些边界的最小测试；下一步应把 Read 通过最小 executor skeleton 串起来，或先建立 prompt boundary skeleton。

## 2026-05-21 续作：prompt、executor、Session ReAct loop 与 reasoning 预判定已完成

本节记录当前最新进展。若与上文较早的“下一步推荐”冲突，以本节为准。

### 本轮已完成内容

- `internal/prompt` 已实现 static/dynamic prompt boundary、assembler、环境信息拼接、memory/project context 动态注入。
- `internal/repl` 已实现 tool executor、tool spec conversion、tool result conversion。
- `internal/repl.Session.RunTurn` 已支持有限多轮 provider/tool ReAct loop。
- `RunTurn` 可选启用 reasoning 预判定：主请求前先由 provider 分类任务场景，再把受控 reasoning guidance 注入本轮 dynamic system prompt。
- `tools/read` 已加入输出上限，并改为分片读取，避免超长单行先整体读入内存。
- `docs/architecture.md` 与 `docs/data-flow-and-prompt.md` 已更新当前架构、执行数据流和 prompt 拼接结构。

### 当前仍未实现

- 真实 Anthropic/OpenAI provider。
- `cmd/runcode chat` 接入 session。
- Bubble Tea TUI。
- 权限策略、MCP、hooks、sub-agents、skills、compaction、telemetry、persistence。
- `Read` 之外的内置工具。

### 已验证命令

```bash
go -C "D:/我的AI/runcode" test ./...
PATH="/c/msys64/ucrt64/bin:$PATH" CGO_ENABLED=1 go -C "D:/我的AI/runcode" test -race ./...
go -C "D:/我的AI/runcode" build ./cmd/runcode
```

### 下一步推荐

先提交当前基础能力快照。提交后推荐继续做 Anthropic provider skeleton：只实现 provider adapter 的最小结构和测试，不接 TUI，不直接改 `chat` 为真实交互。

### 当前状态一句话总结

`runcode` 现在已经具备 provider-neutral 的 tool/prompt/repl/session 基础闭环和可选 reasoning 预判定；下一步应先提交稳定快照，再实现真实 provider skeleton。


## 2026-05-22 续作：Anthropic SDK provider skeleton 已完成

本节记录当前最新进展。若与上文较早的“下一步推荐”冲突，以本节为准。

### 本轮已完成内容

- 新增 `pkg/llm/providers/anthropic` provider skeleton，使用官方 `github.com/anthropics/anthropic-sdk-go v1.45.0`。
- `Provider` 已实现 `llm.Provider`：
  - `Name()` 返回 `anthropic`。
  - `Capabilities()` 声明支持 cache control，不声明 thinking request 支持。
  - `Stream(ctx, req)` 将 neutral request 转为 Anthropic Messages request，并返回 neutral `llm.Stream`。
- 新增 SDK 隔离层 `sdk.go`，SDK client、request option、stream 类型不泄漏到 `internal/repl`。
- 新增 request conversion：
  - system text/cache control
  - user/assistant/tool messages
  - tools
  - assistant `tool_use`
  - `RoleTool` -> user `tool_result`
  - max tokens / temperature
- 新增 stream conversion：
  - message/content block start/delta/stop
  - text delta
  - tool input JSON delta
  - thinking/signature delta 字段
  - usage
  - stop reason 映射
- 新增 provider 包测试：
  - `provider_test.go`
  - `convert_test.go`
  - `stream_test.go`
- 更新 `docs/architecture.md`，说明 Anthropic SDK provider skeleton 已存在但尚未接 CLI/TUI。

### 当前仍未实现

- `cmd/runcode chat` 接入 session/provider。
- 真实 Anthropic API integration test。
- API key 配置、model discovery、pricing/token budget、retry/backoff、telemetry。
- OpenAI provider / OpenAI-compatible provider。
- Bubble Tea TUI。
- 权限策略、MCP、hooks、sub-agents、skills、compaction、persistence。
- `Read` 之外的内置工具。

### 已验证命令

```bash
go -C "D:/我的AI/runcode" mod tidy
go -C "D:/我的AI/runcode" fmt ./pkg/llm/providers/anthropic
go -C "D:/我的AI/runcode" test ./pkg/llm/...
go -C "D:/我的AI/runcode" test ./internal/repl ./pkg/llm/...
go -C "D:/我的AI/runcode" test ./...
PATH="/c/msys64/ucrt64/bin:$PATH" CGO_ENABLED=1 go -C "D:/我的AI/runcode" test -race ./...
go -C "D:/我的AI/runcode" build ./cmd/runcode
```

全部通过。

### 下一步推荐

先查看 diff 并提交当前 provider skeleton。提交后推荐继续做 OpenAI-compatible provider skeleton，仍保持不接 TUI、不直接改 `chat` 为真实交互。

### 当前状态一句话总结

`runcode` 现在已经具备 provider-neutral 的 tool/prompt/repl/session 基础闭环、可选 reasoning 预判定，以及第一个真实 provider adapter skeleton（Anthropic SDK）；下一步应提交稳定快照，再扩展 OpenAI-compatible provider 或开始设计 CLI wiring。

## 2026-05-22 续作：最小 chat CLI 接入已完成

本节记录当前最新进展。若与上文较早的“下一步推荐”冲突，以本节为准。

### 本轮已完成内容

- `cmd/runcode chat` 已从占位命令改为最小 provider-backed single-turn CLI。
- chat prompt 输入支持：
  - `runcode chat "..."` 从命令参数读取。
  - `printf "..." | runcode chat` 从 stdin 读取。
- stdout 只输出最终 assistant text，不再打印 banner，便于 shell 管道使用。
- chat 配置支持：
  - `--model` / `ANTHROPIC_MODEL`
  - `--max-tokens` / `ANTHROPIC_MAX_TOKENS`
  - `--base-url` / `ANTHROPIC_BASE_URL`
  - `--api-key` / `ANTHROPIC_API_KEY`
  - `--auth-token` / `ANTHROPIC_AUTH_TOKEN`
  - `--cwd` / `RUNCODE_CWD`
- `pkg/llm/providers/anthropic.Options` 已新增 `AuthToken`，SDK client 创建时优先使用 `option.WithAuthToken`，否则使用 `option.WithAPIKey`。
- chat 命令会构造：
  - Anthropic provider
  - `tools.Builtins()`
  - `internal/repl.Session`
  - `tool.Context`，其中 `WorkingDirectory` 来自配置，`ReadSet` 初始化为空 map。
- 新增 chat command fake runner seam，单元测试不触网。
- `cmd/runcode/main_test.go` 已替换旧 placeholder 测试，覆盖 args/stdin prompt、空 prompt、缺少 model、缺少 credential、env/flag 配置、flag override、runner error。
- `pkg/llm/providers/anthropic/provider_test.go` 已补充 `AuthToken` 构造测试。
- `docs/architecture.md` 已更新当前 CLI 边界。

### 当前仍未实现

- Bubble Tea TUI。
- 多轮持久 conversation history。
- streaming terminal 输出。
- SQLite transcript / persistence。
- 权限策略；当前 chat 只暴露现有 `Read` 工具，后续接 Write/Edit/Bash 前必须先补权限。
- OpenAI provider / OpenAI-compatible provider。
- model discovery、pricing/token budget、retry/backoff、telemetry。
- `Read` 之外的内置工具。

### 下一步推荐

先运行全量验证并查看 diff，然后提交当前 Anthropic provider + chat CLI 稳定快照。提交后建议二选一：

1. 实现 OpenAI-compatible provider skeleton。
2. 为 chat 增加最小多轮内存 history，但仍不接 TUI。

### 当前状态一句话总结

`runcode` 现在可以通过 `runcode chat` 触发 Anthropic provider、prompt assembler、Session ReAct loop 和当前 `Read` 工具链路；它仍是 single-turn 非 TUI CLI。

## 2026-05-25 续作：observability / telemetry 基础层已完成

本节记录当前最新进展。若与上文较早的“下一步推荐”冲突，以本节为准。

### 本轮已完成内容

- 新增 `internal/telemetry` 内部 observability 基础层：
  - 统一 `Event` / `EventName` / `Attrs` 模型。
  - 事件名覆盖 turn、LLM request、tool execution 生命周期。
  - 属性 key 集中定义，避免各处手写字符串漂移。
  - `Noop` recorder，默认无行为。
  - JSONL recorder，每个 event 一行 JSON。
  - bounded async recorder，非阻塞写入，队列满时 drop event 而不阻塞主流程。
  - memory recorder，用于测试中验证事件顺序和字段。
  - trace / turn / request ID 生成，用于关联一个 chat turn 内的请求和工具调用。
- `internal/repl.Session` 已接入 telemetry：
  - `turn.start`
  - `turn.end`
  - `turn.error`
  - `llm.request.start`
  - `llm.request.end`
  - `llm.request.error`
- `internal/repl.Executor` 已接入 telemetry：
  - `tool.execute.start`
  - `tool.execute.end`
  - `tool.execute.error`
- `cmd/runcode chat` 已新增 telemetry 配置：
  - `--telemetry off|jsonl`
  - `RUNCODE_TELEMETRY=off|jsonl`
  - 默认 `off`。
  - `jsonl` / `stderr-jsonl` 输出到 stderr，stdout 仍只保留 assistant text。
- telemetry 数据边界：
  - 不记录 prompt 原文。
  - 不记录 assistant 原文。
  - 不记录 tool input JSON 原文。
  - 不记录 tool output / 文件内容。
  - 不记录 API key、auth token、base URL。
  - 只记录长度、数量、名称、stop reason、usage、duration、错误字符串和 correlation ID。
- 已补充测试：
  - `internal/telemetry` recorder/event/id 行为。
  - `internal/repl` session/executor telemetry 事件。
  - `cmd/runcode` telemetry config 解析。
- `docs/architecture.md` 已更新 observability 边界。

### 当前仍未实现

- 外部 collector / OpenTelemetry。
- SQLite transcript / 持久化日志。
- 数字飞轮数据平台。
- telemetry sampling / session id / 用户级聚合。
- Bubble Tea TUI。
- 多轮持久 conversation history。
- 权限策略与更多工具。

### 当前状态一句话总结

`runcode` 现在具备一套内部统一 telemetry 事件模型和非阻塞本地 JSONL 输出能力，可用于验证 chat/session/provider/tool 主链路，并为后续数字飞轮预留低耦合扩展点。

## 2026-05-25 续作：权限策略基础层已完成

本节记录当前最新进展。若与上文较早的“下一步推荐”冲突，以本节为准。

### 本轮已完成内容

- 新增 `internal/permissions` 内部权限基础层：
  - action / operation / risk 模型。
  - resource type / scope 模型。
  - allow / ask / deny decision 模型。
  - default resolver，可解析当前 `Read` 工具输入。
  - default policy：workspace 内 `Read` allow，workspace 外 `Read` deny，unknown deny，未来高风险操作按 ask/deny 建模。
  - non-interactive authorizer：`ask` 在当前 CLI 环境中转为 deny。
  - service facade，供 executor 统一调用。
  - permission telemetry helper，只记录脱敏结构化元数据。
  - 路径解析通过 `internal/toolpath` 共享，权限层会解析已存在 symlink 目标后判断 workspace containment。
- `internal/repl.Executor` 已接入权限检查：
  - tool run 前统一 authorize。
  - allow 才调用真实工具。
  - deny 不调用工具，返回 `tool.Result{IsError: true}`。
  - permission denial 不作为 Go error 中断 turn。
- `internal/repl.Session` 已支持注入 `Permissions *permissions.Service`，并通过 executor 统一执行权限边界。
- `pkg/tool.Result` 新增 `IsError`。
- `pkg/llm.ContentBlock` 新增 `IsError`。
- `ToolResultBlock` 会把 tool result error 语义传给 LLM neutral content block。
- Anthropic provider conversion 已把 neutral tool_result `IsError` 映射到 SDK `tool_result.is_error`。
- `internal/telemetry` 新增 `permission.decision` 事件和相关 attrs；tool error telemetry 改为记录受控错误类别，避免泄露真实路径。
- `cmd/runcode chat` 新增权限配置：
  - `--permission-mode safe`
  - `RUNCODE_PERMISSION_MODE=safe`
  - 当前只支持 `safe`，不提供默认 allow-all。
- `docs/architecture.md` 已更新 permission boundary。

### 当前仍未实现

- Write/Edit/Bash 工具。
- 交互式权限审批 UI。
- 持久化 allowlist / denylist。
- MCP 权限策略。
- session 级权限记忆。
- allow-all / dangerously allow 模式。

### 当前状态一句话总结

`runcode` 现在具备 executor 前统一权限边界，默认安全策略会允许 workspace 内 Read、拒绝越界或未知工具，并把权限拒绝作为 `is_error` tool result 回传模型，同时通过脱敏 telemetry 记录 decision。

## 2026-05-26 续作：Write/Edit mutation boundary 已完成

本节记录当前最新进展。若与上文较早的“当前仍未实现”冲突，以本节为准。

### 本轮已完成内容

- `pkg/tool.ReadFile` 新增 `Complete bool`，用于区分完整读取和部分读取。
- `tools/read` 更新 `ReadSet` 语义：完整读取标记 `Complete=true`，offset/limit 或截断导致的部分读取标记 `Complete=false`。
- `internal/toolpath` 新增 mutation target 解析：
  - 目标存在时检查真实 target 是否在 workspace 内。
  - 目标不存在时要求父目录存在，并检查真实父目录在 workspace 内。
  - workspace 内 symlink/junction 指向 workspace 外会被识别为 outside。
  - 不自动创建父目录。
- `internal/toolpath.RequireFreshRead` 新增运行期 read gate：overwrite/edit 前必须有完整 `ReadSet`，且当前文件 size/modtime 与读取记录一致。
- `internal/permissions` 扩展 Write/Edit mutation 语义：
  - `Write` 区分 `create` 和 `overwrite`。
  - `Edit` 第一版只支持 exact `replace` 语义。
  - metadata 记录 `mutation_kind`、`read_requirement`、`read_state`、`target_exists`，不记录 raw path 或内容。
  - default policy 对 workspace 内 create/fresh overwrite/fresh replace 返回 ask；当前 safe non-interactive authorizer 仍会最终 deny。
  - missing/partial/stale read、invalid target、outside workspace 都会 deny。
- 新增 `tools/write`：
  - 创建新文件。
  - 覆盖已有文件前要求 fresh complete read。
  - 工具运行阶段重复 mutation target 和 read gate 检查。
- 新增 `tools/edit`：
  - exact `old_string` -> `new_string` 替换。
  - 默认要求 `old_string` 唯一。
  - `replace_all=true` 时替换全部。
  - old_string 缺失、多处未声明 replace_all、old==new、未读/部分读/stale 都会拒绝且不写文件。
- `tools.Builtins()` 现在注册 `Read`、`Write`、`Edit`，因此 prompt/tool specs/executor 继续同源。
- `internal/repl` 测试覆盖 default safe 下 Write 不会实际运行，以及测试注入 allow policy 时 Write 可执行。

### 当前仍未实现

- 交互式权限审批 UI；当前 safe non-interactive 下 Write/Edit 会被建模为 ask 后最终 deny。
- 持久化 allowlist / denylist。
- session 级权限记忆。
- Bash/TodoWrite/MCP/hooks/sub-agents/skills。
- 多轮持久 conversation history。
- Edit append/insert/regex patch/line patch。
- hash-based stale check。

### 当前状态一句话总结

`runcode` 现在已有真实 Read/Write/Edit 内置工具和统一 mutation 权限语义；默认 safe CLI 仍不会无审批写文件，但路径解析、写前必须完整读取、stale read 检查和脱敏 telemetry 边界已经落位。

## 2026-05-26 续作：交互式权限审批已完成

本节记录当前最新进展。若与上文较早的“当前仍未实现”冲突，以本节为准。

### 本轮已完成内容

- `internal/permissions` 新增 approval model：
  - `ApprovalRequest`
  - `ApprovalResponse`
  - `Approver`
  - `InteractiveAuthorizer`
- `safe` 模式保持默认安全行为：policy 返回 `ask` 时最终仍 deny，不执行 mutation。
- `interactive` / `confirm` 模式已接入 CLI：
  - `--permission-mode interactive`
  - `--permission-mode confirm`
  - `RUNCODE_PERMISSION_MODE=interactive`
- `InteractiveAuthorizer` 只处理 policy 已判定为 `ask` 的 action：
  - approval allow => final effect 为 allow，reason 为 `approval_granted`。
  - approval deny => final effect 为 deny，reason 为 `approval_denied`。
  - approver 缺失、prompt error、EOF、context cancel 等不可用场景安全 deny，reason 为 `approval_unavailable` 或 `approval_denied`。
- `cmd/runcode` 新增 stderr approval prompter：
  - 审批提示写 stderr，不污染 stdout。
  - 审批输入从 stdin 读取。
  - 支持 `y` / `yes` / `allow`。
  - 支持空行 / `n` / `no` / `deny`。
  - 无效输入最多重试 3 次，超过后 deny。
- 审批提示只展示脱敏摘要：tool name、operation、risk、resource type/scope/count、mutation kind、read state、target exists、policy rule。
- 审批提示不展示 raw path、tool input、file content、old_string/new_string、credential 或 base URL。
- `cmd/runcode chat` runner seam 已增加 `chatIO{In, Err}`，避免把 Cobra command 传入 runner，同时让测试可注入 stdin/stderr。
- `internal/repl.Executor` 已用真实 interactive permission service 覆盖：
  - ask + approval allow 时 `Write` 实际执行。
  - ask + approval deny 时工具不运行，返回 `is_error=true` tool result。
  - permission telemetry 记录 final effect/reason，且不泄露 path/content。

### 当前仍未实现

- 持久化 allowlist / denylist。
- session 级 permission memory。
- TUI / 远程审批 UI。
- Bash/TodoWrite/MCP/hooks/sub-agents/skills。
- 多轮持久 conversation history。
- Edit append/insert/regex patch/line patch。
- hash-based stale check。

### 当前状态一句话总结

`runcode` 现在已经具备 safe 与 interactive 两种权限模式；interactive 模式可对 workspace 内合规 mutation 做一次性审批，拒绝、EOF、取消和异常都会安全失败，同时 stdout/stderr 与 telemetry 脱敏边界保持清晰。

## 2026-05-26 续作：内存多轮会话历史与 chat loop 已完成

本节记录当前最新进展。若与上文较早的“当前仍未实现”冲突，以本节为准。

### 本轮已完成内容

- `internal/repl.Session` 新增进程内 `llm.Message` 历史：
  - 每轮 `RunTurn` 会 clone 当前 history，再追加本轮 user message。
  - 同一 turn 内的 assistant `tool_use` message 和 tool result message 会继续进入下一次 provider request。
  - turn 正常完成后提交完整消息链到 session history。
  - turn 出错时不提交失败轮次，避免半截 tool call 污染后续上下文。
- `Session` 新增历史管理 API：
  - `History()` 返回 clone，防止外部修改内部历史。
  - `ResetHistory()` 清空进程内历史，供后续 TUI、命令和测试复用。
- `cmd/runcode chat` 新增 `--loop`：
  - args 会作为第一轮 prompt。
  - 后续从 stdin 按行读取 prompt。
  - EOF、`/exit`、`/quit`、`exit`、`quit` 会正常退出。
  - 空行会跳过。
  - 每轮 assistant final text 仍写 stdout。
  - prompt marker 写 stderr。
- CLI default runner 现在在同一个 command 生命周期内复用同一个 `repl.Session`，因此 `chat --loop` 可保留进程内上下文。
- 新增共享 stdin line reader：
  - `chat --loop` 和 interactive approval 复用同一个 line reader。
  - 避免 loop prompt 和 approval prompt 各自包 `bufio.Reader` 导致 buffered stdin 被预读丢失。
  - context cancel 可让读取方及时返回。
- approval prompter 已改为依赖共享 `lineReader`，连续审批不会丢失已缓冲输入。
- `docs/architecture.md` 已同步当前架构：Session 内存 history、`chat --loop`、stdout/stderr 边界和验证矩阵。

### 当前仍未实现

- SQLite transcript / 持久化 conversation history。
- history compaction / token budget trimming。
- Bubble Tea TUI。
- streaming terminal output。
- slash command 系统。
- 持久化 permission allowlist / denylist。
- session 级 permission memory。
- Bash/TodoWrite/MCP/hooks/sub-agents/skills。

### 当前状态一句话总结

`runcode` 现在已经支持 provider-neutral Session 的进程内多轮历史，并提供最小 `runcode chat --loop` 入口复用同一个 session；它仍不持久化 transcript，也暂未实现 TUI、streaming 输出或压缩。

## 2026-05-26 续作：Glob/Grep 只读搜索工具已完成

本节记录当前最新进展。若与上文较早的“当前仍未实现”冲突，以本节为准。

### 本轮已完成内容

- 新增 `tools/glob`：
  - 支持 workspace 内文件发现。
  - 支持 slash-separated glob pattern，包括 `**` recursive segment。
  - Win11/Linux/macOS 行为保持一致：输出 workspace-relative slash 路径。
  - 支持 `path` 搜索根、`limit` 截断、context cancellation。
  - 只返回文件，不返回目录，不更新 `ReadSet`。
- 新增 `tools/grep`：
  - 使用 Go regexp 搜索 workspace 内文本文件。
  - 支持 `path` 文件/目录搜索、`glob` 文件过滤、`case_insensitive`、`limit` 截断。
  - 输出格式为 `relative/path:line:content`。
  - 跳过二进制文件，不更新 `ReadSet`。
- `tools.Builtins()` 现在注册：
  - `Read`
  - `Write`
  - `Edit`
  - `Glob`
  - `Grep`
- `internal/permissions` 已接入只读搜索权限：
  - workspace 内 `Glob` / `Grep` allow。
  - outside workspace、symlink escape、invalid input、missing pattern deny。
  - 不影响 Write/Edit 的 fresh-read gate。
- `docs/architecture.md` 已同步工具清单、权限边界和验证矩阵。

### 当前仍未实现

- Bash/TodoWrite/MCP/hooks/sub-agents/skills。
- Bubble Tea TUI。
- SQLite transcript / 持久化 conversation history。
- history compaction / token budget trimming。
- 持久化 permission allowlist / denylist。
- Grep context-before/context-after 输出。
- `.gitignore` 完整语义。

### 当前状态一句话总结

`runcode` 现在已有 Read/Write/Edit/Glob/Grep 五个内置工具，模型可以先用 Glob/Grep 在 workspace 内发现和搜索代码，再用 Read 查看完整文件、用 Edit/Write 在审批边界内修改文件。

## 2026-05-27 续作：Bash 前命令权限分类已完成

本节记录当前最新进展。若与上文较早的“当前仍未实现”冲突，以本节为准。

### 本轮已完成内容

- `internal/permissions` 新增命令分类模型：
  - command category：read-only、test、build、package manager、network、VCS、destructive VCS、workspace write、outside write、privileged、unknown。
  - command capabilities：reads workspace、writes workspace、writes outside、uses network、modifies VCS、destructive VCS、requires privilege、unknown effects。
  - command risk reasons：unknown command、shell control operator、redirect output、package manager、network access、destructive VCS、outside workspace write、privileged command、workspace write。
  - 分类结果只包含受控标签和 bounded summary，不保存 raw command 或 raw args。
- `Bash` resolver 预留已接入权限层，但仍未实现或注册真实 Bash 工具：
  - `command` 必填。
  - invalid/missing command deny。
  - 解析后生成 `OperationExecute` action、`ResourceCommand` resource 和脱敏 command metadata。
- default execute policy 已从统一 ask 改为分类决策：
  - 低/中/高风险的已分类非硬拒绝命令需要 approval。
  - safe non-interactive 模式仍把 ask 转为 deny。
  - critical、unknown effects、requires privilege、writes outside、destructive VCS 直接 deny，interactive approval 也不能绕过。
  - 当前没有任何 Bash 命令会自动 allow。
- approval summary / CLI prompt 已显示脱敏命令信息：
  - command category。
  - command capabilities。
  - command risk reasons。
  - command summary。
  - 不显示 raw command、URL、env、credential、路径或完整参数。
- permission telemetry 已新增脱敏 command attrs：
  - `command_category`
  - `command_capabilities`
  - `command_risk_reasons`
  - `command_summary`
  - 仍不记录 raw command、URL、路径、tool input/output 或文件内容。
- `docs/architecture.md` 已同步 Bash 前命令权限分类边界。

### 当前仍未实现

- 真实 `tools/bash`。
- 将 Bash 注册到 `tools.Builtins()`。
- shell 执行器、timeout、background task、stdout/stderr streaming。
- 持久化 permission allowlist / denylist。
- session 级 permission memory。
- TodoWrite/MCP/hooks/sub-agents/skills。
- Bubble Tea TUI。

### 当前状态一句话总结

`runcode` 现在还不会执行 Bash，但权限系统已经能对未来 Bash 输入做保守分类、硬拒绝高危命令，并为审批提示和 telemetry 提供脱敏摘要，为后续接入真实 Bash 工具打好了安全边界。

## 2026-05-27 续作：最小安全 Bash 工具已完成

本节记录当前最新进展。若与上文较早的“当前仍未实现”冲突，以本节为准。

### 本轮已完成内容

- 新增 `tools/bash` 内置工具：
  - Tool name 为 `Bash`。
  - input schema 支持 `command` 和可选 `timeout_ms`。
  - 固定在 workspace root 执行，不支持自定义 cwd。
  - 不接 stdin，避免交互式命令挂起等待输入。
  - 使用 context/timeout 控制命令生命周期。
  - 捕获 stdout/stderr，输出总量有上限，超过会截断并标记。
  - 非零 exit code、timeout、cancel 返回 `tool.Result{IsError: true}`，不把普通命令失败升级为 executor Go error。
  - result metadata 只包含 `exit_code`、`timed_out`、`duration_ms`、`truncated` 这类受控字段。
- `tools.Builtins()` 已注册 `Bash`，现在内置工具为：
  - `Read`
  - `Write`
  - `Edit`
  - `Glob`
  - `Grep`
  - `Bash`
- `internal/repl.Executor` 仍是执行前唯一授权入口：
  - safe 模式下 Bash ask 会最终 deny，不会运行。
  - interactive approval allow 后，非硬拒绝 Bash 命令可以运行。
  - hard deny 命令即使 interactive allow 也不会运行。
- session tool spec 测试已同步包含 `Bash`。
- `docs/architecture.md` 已同步 Bash MVP runtime 限制和验证矩阵。

### 当前仍未实现

- Bash background task。
- streaming stdout/stderr UI。
- 自定义 cwd/env。
- 交互式 stdin。
- 持久化 command allowlist / denylist。
- session 级 permission memory。
- TodoWrite/MCP/hooks/sub-agents/skills。
- Bubble Tea TUI。

### 当前状态一句话总结

`runcode` 现在已经有真实 Bash 内置工具，但它仍被权限系统默认安全地包住：safe 模式不会执行，interactive 只能审批非硬拒绝命令，运行期也限制在 workspace cwd、无 stdin、timeout 和输出截断边界内。

---

## 2026-06-07 续作：Sub-agents 子代理系统已完成

> 注：本 handoff 日志在 Bash 工具轮后未持续记录。其间落地的 MCP（tools/resources/prompts/roots/sampling 全原语）、skills、hooks、OpenAI 兼容 provider、context compaction、cost tracking、slash commands、权限持久化等，权威记录见 `CHANGELOG.md`。本章节仅记录 sub-agents 一轮。

### 本轮目标

让主代理能把独立、自洽的任务委托给专注的子代理（对标 Claude Code 的 Task / sub-agent），复用现有 `repl.Session` 作为子代理运行时。

### 设计与分层（高内聚、低耦合、无循环依赖）

- `pkg/agent`（纯定义层，镜像 `pkg/skill`）：`agent.go` / `parse.go` / `load.go`。Agent = name / description / 可选 tools 白名单 / 可选 model + body(系统提示)。约定目录发现、frontmatter 解析、容错加载、catalog 渲染、工具策略（`*`/省略=继承全部）。
- `internal/subagent`（运行时层）：`launcher.go`（用受限工具集 + persona + 指定模型构建并运行子 `repl.Session`）、`tool.go`（`Task` 工具）、`default.go`（内置 `general-purpose`）、`events.go`（子代理工具事件 → 归属父 `Task` 行的进度桥接）。
- `cmd/runcode`：`agents.go`（约定目录 + builtin 合并）、`chat.go` 装配（捕获“不含 Task”的可委托工具集 → 构建 Launcher → 追加 `Task` 工具并把 agent catalog 注入 prompt；TUI 共用此路径）、`config.go`（列出可用子代理）。
- prompt 装配：`AssemblerOpts` 新增 `Agents`(父会话 catalog) 与 `AgentInstructions`(子会话 persona)。
- 权限：`Task` 归类 `OperationManage` / `RiskLow`（编排本身免审批；真正门控在每个子工具调用上）。

### 关键设计决策

- **不嵌套**：子代理永不获得 `Task` 工具，委托恰好一层深。
- **权限统一**：子会话共用父权限服务；PreToolUse/PostToolUse 钩子对子工具仍逐一生效，但**屏蔽 UserPromptSubmit**（子任务 prompt 是内部委托，非用户输入）。
- **临时性**：子会话不持久化 transcript / 可恢复 session，但记录独立 trace 的 telemetry。
- **优先级 user > project > builtin**；agents 目录中的 `README.md` 视作文档而跳过。
- 项目级 agents 经 `.gitignore` 放行在仓库内共享（对等 `.runcode/skills/`）；可复制示例置于 `examples/agents/`。
- **v1 串行**（`Task` 非并发安全）；并行 fan-out、跨 provider、按 agent 上下文预算留待后续。

### 顺带修复的语义 bug

`loadAgents` 合并顺序错误：原先把 builtin `append` 在最前，而 `NewSet` 是“先插入者胜”，会让内置 `general-purpose` 反向覆盖用户同名定义。改为 `append(discovered.All(), BuiltinAgents()...)`，确立正确优先级 user > project > builtin，并修正三处自相矛盾的注释。

### 已验证命令

```bash
go build ./...
go vet ./...
go test ./...
go run ./cmd/runcode config   # 输出含 “agents: general-purpose”
```

### 测试覆盖

- `pkg/agent`：frontmatter 解析、容错加载、README 跳过、去重/排序/优先级、工具策略、catalog、仓库示例解析。
- `internal/subagent`：launcher 工具集过滤、模型覆盖、persona 进系统提示且不泄露父 catalog、`Task` 输入校验与代理解析、hook 作用域（仅工具钩子）、事件桥转发与 nil-parent 不死锁。
- `cmd/runcode`：`agentRoots` 顺序/降级、`loadAgents` 始终含 builtin、项目发现、user 覆盖 builtin。

### 当前注意事项

- `prompts/agents/` 仍是占位空目录：约定加载目录是 `<workspace>/.runcode/agents/` 与 `<userConfigDir>/runcode/agents/`，并非 `prompts/agents/`；`examples/agents/` 是模板，不会被自动加载。
- 本轮已同步 `docs/architecture.md`、`docs/implementation-status.md`、`docs/data-flow-and-prompt.md` 中与 sub-agents 相关的条目，但这些状态文档可能仍残留更早功能的过时描述；权威记录以 `CHANGELOG.md` 为准。

### 当前状态一句话总结

主代理现在可经内置 `Task` 工具把任务委托给命名子代理：子代理在受限工具集、独立上下文、共用父权限/钩子的临时子会话中自主完成任务并返回单条报告，委托恰好一层、不嵌套，安全语义与主会话一致。
