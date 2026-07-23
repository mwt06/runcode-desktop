# CLAUDE.md

## 协作原则

- 不要机械追求“最小改动”。实现方案应优先保证系统的可靠稳定、可扩展、性能可控、高内聚、低耦合。
- 对简单 bug 或明确小需求，可以保持小范围修改；但如果“最小补丁”会造成脆弱设计、重复逻辑、抽象泄漏、后续难扩展或性能隐患，应主动提出并实现更稳健的基础层。
- 规划功能时要说明取舍：哪些是当前必须做的可靠性/扩展性基础，哪些是暂不做的过度工程。
- 默认避免无关扩张，但不要为了少改几行牺牲长期架构质量。

## 构建与打包

引擎已独立成仓：**`gitlab.ouc-online.com.cn/aibase/agentloop`**（require 固定 tag，无 replace）。本地开发用同级 checkout `../agentloop` 经 `go.work` 联动（改引擎实时生效）；`GOWORK=off` 构建则设 `GOPRIVATE=gitlab.ouc-online.com.cn` 直连内网 GitLab 拉取 tag。改引擎（ReAct 循环、工具、权限、provider、持久化等）去 agentloop 仓库；本仓库只改外壳。

本仓库含**三个 Go module**（依赖方向：外壳 → agentloop，反向不存在）：

1. **根模块（`github.com/wt68/runcode`）**——CLI/TUI（`cmd/runcode`、`internal/ui`、`pkg/command`）+ 桌面核心（`internal/desktop`）+ `tools/preview`、`tools/protogen`。
2. **桌面外壳（`cmd/runcode-desktop`，嵌套 module）**——Wails/CGO 重依赖隔离层。
3. **服务端骨架（`cmd/runcode-server`，嵌套 module）**——独立仓库服务端的可跑参考实现。

`go.work` 已提交（use：`.`、`./cmd/runcode-desktop`、`./cmd/runcode-server`、`../agentloop`）供本地联动；**CI/发布链路一律 `GOWORK=off`**，此时引擎按 go.mod require 的 tag 版本经 `GOPRIVATE` 从内网 GitLab 解析——引擎改动须打新 tag 并升 require 才进 CI/发布（GitHub CI 够不着内网 GitLab，见 `.github/workflows/ci.yml` 顶部 TODO）。

### 常用命令（根目录执行）

- 全量编译：`go build ./... ; go -C cmd/runcode-server build ./... ; go -C cmd/runcode-desktop build ./...`（或 `make build`）。
- 测试（CI 用 `-race`，三平台）：`go test -race ./... ; go -C cmd/runcode-server test -race ./...`（或 `make test`）。
- Lint：`golangci-lint run`（或 `make lint`；配置根 `.golangci.yml`，启用 gosec/errcheck/gocritic）。
- 协议 TS 再生成（引擎 protocol 变更后）：`go run ./tools/protogen`；CI 用 `--check` 防漂移。
- 出 CLI 二进制：`go build -o runcode.exe ./cmd/runcode`。

### `internal/` 文件分工

两个包，各自一个包内按职责分文件（Go 里"目录结构"就是包，包内靠文件名分工）：

- **`internal/desktop`**（桌面核心，Wails 把 `App` 的导出方法绑给前端，所以命令必须都挂在同一个类型上，不能拆包）：`app.go` 只留 App 结构与会话开关；回合在 `turn.go`、自动标题在 `title.go`、运行中可变的会话设置在 `session_settings.go`；其余按功能各自成文件（`skills` / `agents` / `mcp` / `passport` / `oauth` / `tokens` / `preview` / `editstore` / `custommodels` / `config` / `store` / `disabled` / `harm` / …）。技能与子代理共用的作用域目录解析与命名规则在 `resources.go`（`resourceRoot(kindSkills|kindAgents, scope)`），别再各写一份。
- **`internal/ui`**（CLI 的 TUI）：`model.go` 是 bubbletea 生命周期；工具事件归并在 `tool_events.go`、异步命令工厂在 `tea_commands.go`（与 `slash_commands.go` 的斜杠命令是两回事）；渲染分 `render.go`（组装）/ `render_approval.go` / `render_tools.go` / `markdown.go` / `format.go`，调色板集中在 `render.go`。包说明见 `doc.go`。

自检：`go build ./...`、`go test -race ./internal/... ./cmd/runcode/...`、`golangci-lint run ./internal/...`。**注意 lint 有既有基线**（gosec 的 G304/G301、测试里的 errcheck/bodyclose 约 77 条未清），比对时看增量而非绝对数。

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

#### 前端目录（`cmd/runcode-desktop/frontend/src`）

跨目录一律用 `@/` 别名（`vite.config.ts` 的 `resolve.alias` + `tsconfig.json` 的 `paths`），同目录内保持 `./` 相对路径——这样挪文件不牵动导入方。分层自下而上，依赖只朝下：

| 目录 | 职责 |
| --- | --- |
| `core/` | 后端通道与领域纯逻辑：`bridge`、生成的 `protocol/`、`paths`、`format`、`tool-catalog`（内置工具中文目录）、`custom-models`、`passport-account` |
| `ui/` | 与业务无关的通用件：`tokens`（BTN/DRAG 等类名常量）、`icons`、`markdown`、`popover`、`fields`、`model-picker`、`badges`、`glyphs`、`toggle`、`confirm-dialog`… |
| `hooks/` | 跨模块通用钩子：`use-stick-to-bottom`、`use-persistent-state` |
| `chat/` | 对话渲染层：`blocks`（分组/合并纯逻辑）、`tool-text`（工具事件→中文，纯函数）+ 各类卡片组件 |
| `preview/` | 预览：`classify`/`tabs`（纯逻辑）、`file-panel`/`diff-panel`/`pane`/`file-browser`，Office 查看器在 `viewers/` |
| `composer/` | 输入区：`mention`（触发解析与候选排序，纯函数）、`mention-picker`、`toolbar`、`index` |
| `pages/` | 整页：`plugins/`、`permissions`、`mcp`、`memory`、`start/`、`settings/` |
| `session/` | 应用状态与副作用钩子：`use-conversation`（引擎事件订阅在此）、`use-session`、`use-permission-queue`、`use-preview-panel`、`use-workspace-files`、`use-auto-preview`、`use-toast` |
| `shell/` | 外壳组件：`title-bar`、`status-bar`、`chat-pane`、`preview-side`、`permission-modal`、`sidebar` |
| `dev/` | 样式预览页（`?preview=tools` / `?preview=thinking`），不进正常流程 |

`App.tsx` 只负责把上述钩子接起来并按视图摆放 shell 组件，不放具体逻辑。改行为找 `session/`，改样子找 `shell/` 或对应页面。

前端自检（在 `frontend/` 下）：`npm run typecheck`、`npx vitest run`、`npm run build`。纯逻辑模块（`chat/tool-text`、`chat/blocks`、`preview/classify`、`preview/tabs`、`composer/mention`、`pages/mcp-draft`、`core/custom-models`、`core/passport-account`）都有单测，新增纯函数请一并补测。
