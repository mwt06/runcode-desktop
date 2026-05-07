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
- Go module：`github.com/your-username/runcode`
  - `your-username` 是占位符，后续创建 GitHub 仓库前需要替换为实际 owner。
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
module github.com/your-username/runcode

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
- `README` 中 GitHub URL 仍是 `github.com/your-username/runcode`，后续替换 owner。
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

注意：仓库没发布前，workflow 中 `your-username` 不影响本地构建，但后续要替换。

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
- release owner 仍为 `your-username`

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
?    github.com/your-username/runcode/cmd/runcode [no test files]

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
5. `your-username` 是全仓占位符，后续确定 GitHub owner 后要替换。
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
?    github.com/your-username/runcode/cmd/runcode [no test files]
ok   github.com/your-username/runcode/pkg/llm
ok   github.com/your-username/runcode/pkg/tool
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
   module github.com/your-username/runcode
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
