# CLAUDE.md

## 协作原则

- 不要机械追求“最小改动”。实现方案应优先保证系统的可靠稳定、可扩展、性能可控、高内聚、低耦合。
- 对简单 bug 或明确小需求，可以保持小范围修改；但如果“最小补丁”会造成脆弱设计、重复逻辑、抽象泄漏、后续难扩展或性能隐患，应主动提出并实现更稳健的基础层。
- 规划功能时要说明取舍：哪些是当前必须做的可靠性/扩展性基础，哪些是暂不做的过度工程。
- 默认避免无关扩张，但不要为了少改几行牺牲长期架构质量。

## 构建与打包

仓库含**三个 Go module**（依赖方向：外壳 → 根模块 → engine，反向即错误）：

1. **`engine/`（嵌套 module，`github.com/wt68/runcode/engine`）**——传输无关的会话引擎，是三端（CLI/TUI、桌面、未来服务端）共用的公共引用。公开包＝协议+服务面（llm/tool/turn/permissions/mcp/sessions/tools/...）；`engine/internal/` 收 ReAct 循环等内核，对模块外（含同仓兄弟模块）不可见。**engine 永不依赖根模块，也永不引入 Redis/网关等远程基础设施依赖**（端口在引擎、实现在外壳）。
2. **根模块（`github.com/wt68/runcode`）**——CLI/TUI（`cmd/runcode`、`internal/ui`、`pkg/command`）+ 桌面核心（`internal/desktop`）+ `tools/preview`。
3. **桌面外壳（`cmd/runcode-desktop`，嵌套 module）**——Wails/CGO 重依赖隔离层。

`go.work` 已提交供本地三模块联动；**CI/发布链路一律 `GOWORK=off`**，go.mod 的 replace 链是权威（根与 desktop 各自 replace 指向 `./engine`，replace 不跨模块传递）。

### 常用命令（根目录执行）

- 全量编译：`go build ./... ; go -C engine build ./... ; go -C cmd/runcode-desktop build ./...`（或 `make build`）。
- 测试（CI 用 `-race`，三平台）：`go test -race ./... ; go -C engine test -race ./...`（或 `make test`）。
- Lint：根与 engine 分别 `golangci-lint run`（或 `make lint`；配置共用根 `.golangci.yml`，启用 gosec/errcheck/gocritic）。
- **依赖方向审计**（必须空输出，CI 强制）：`go -C engine list -deps ./... | grep 'wt68/runcode' | grep -v 'wt68/runcode/engine'`（或 `make audit`）。
- 出 CLI 二进制：`go build -o runcode.exe ./cmd/runcode`。

### 桌面版（Wails，`cmd/runcode-desktop`）

- 桌面是**嵌套 Go module**（`cmd/runcode-desktop/go.mod`），用 `replace github.com/wt68/runcode => ../..` 指回核心，把 Wails/CGO/WebView 重依赖隔离在核心之外；核心的 `go build ./...` 与 CI 不会拉 Wails。模块路径仍在 `github.com/wt68/runcode/...` 下，故可复用核心的 `internal/` 包。
- **正式打包**（产出可发布 exe，需已装 `wails` CLI，本机验证 v2.12.0；会跑 `npm install` + `npm run build` 重建前端）：
  ```bash
  cd cmd/runcode-desktop && wails build
  ```
  产物：`cmd/runcode-desktop/build/bin/XRUN.exe`（应用名/输出名 `XRUN` 来自 `wails.json` 的 `outputfilename`）。
- **仅 Go 侧快速编译检查**（不打包、不重建前端）：`go -C cmd/runcode-desktop build ./...`。
- 跨平台：Wails **不能交叉编译**（各 OS WebView 不同——Windows WebView2 / Linux WebKitGTK / macOS WKWebView），需在目标平台原生构建；CI 见 `.github/workflows/desktop.yml`（Linux 加 `-tags webkit2_41 -clean`）。
- `*.exe`（`XRUN.exe`、根目录的 `runcode-desktop.exe` 等）是 `.gitignore` 的构建产物，不进版本库。
