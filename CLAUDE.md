# CLAUDE.md

## 协作原则

- 不要机械追求“最小改动”。实现方案应优先保证系统的可靠稳定、可扩展、性能可控、高内聚、低耦合。
- 对简单 bug 或明确小需求，可以保持小范围修改；但如果“最小补丁”会造成脆弱设计、重复逻辑、抽象泄漏、后续难扩展或性能隐患，应主动提出并实现更稳健的基础层。
- 规划功能时要说明取舍：哪些是当前必须做的可靠性/扩展性基础，哪些是暂不做的过度工程。
- 默认避免无关扩张，但不要为了少改几行牺牲长期架构质量。

## 构建与打包

引擎已独立成仓：**`gitlab.ouc-online.com.cn/aibase/agentloop`**（require 固定 tag，无 replace）。本地开发用同级 checkout `../agentloop` 经 `go.work` 联动（改引擎实时生效）；`GOWORK=off` 构建则设 `GOPRIVATE=gitlab.ouc-online.com.cn` 直连内网 GitLab 拉取 tag。改引擎（ReAct 循环、工具、权限、provider、持久化等）去 agentloop 仓库；本仓库只改外壳。

本仓库含**三个 Go module**（依赖方向：外壳 → agentloop，反向不存在）：

1. **根模块（`github.com/wt68/runcode`）**——CLI/TUI（`cmd/runcode`、`internal/ui`、`internal/command`）+ 桌面核心（`internal/desktop`、`internal/protocol`）+ 桌面专属 host 工具（`internal/previewtool` = `open_preview`、`internal/officetool` = `ReadOffice`、`internal/plantool` = `plan_write`，均经 `engine.Options.ExtraTools` 只在桌面注册；`internal/skilltool` 走另一条路——经 `engine.Options.SkillTool` **替换**内置 Skill 工具，因为会话内工具名唯一，同名工具只能换不能加）+ `tools/protogen`。
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

两个大包，各自一个包内按职责分文件（Go 里"目录结构"就是包，包内靠文件名分工），外加一个只放类型的 `internal/protocol`：

- **`internal/protocol`**（桌面自己的 wire 类型：设置表单、通行证、技能/子代理/MCP/工具管理页、编辑复审、harm 提示）。**加字段、加 DTO 请加在这里，不要加进引擎**——引擎的 `agentloop/protocol` 只负责"跑一个回合"的契约（assistant delta / 工具事件 / 审批 / 回合结果 / 会话状态 / 错误 / envelope），且与 `cmd/runcode-server` 共享。判据是"谁产生它"：引擎 `host` 包产生或消费的归引擎，只有本外壳用的归这里，**没有例外**。命令清单 `CommandKinds` 就在这里（`internal/protocol/commands.go`）——**新增一个 Wails 命令只改本仓**，引擎不必发版；引擎只保留分类词汇 `protocol.CommandKind` 与三个常量（"query 意味着什么"两端必须一致，"有哪些命令"各自声明，`cmd/runcode-server` 另有自己的一份）。两个包由 `tools/protogen` 合并生成同一份 TS，重名会直接报错。
- **`internal/desktop`**（桌面核心，Wails 把 `App` 的导出方法绑给前端，所以命令必须都挂在同一个类型上，不能拆包）：`app.go` 只留 App 结构与会话开关；回合在 `turn.go`、自动标题在 `title.go`、运行中可变的会话设置在 `session_settings.go`；其余按功能各自成文件（`skills` / `agents` / `mcp` / `passport` / `oauth` / `tokens` / `preview` / `editstore` / `plan`（阶段化计划模式的阶段机与审批闸门）/ `custommodels` / `config` / `store` / `disabled` / `harm` / …）。技能与子代理共用的作用域目录解析与命名规则在 `resources.go`（`resourceRoot(kindSkills|kindAgents, scope)`），别再各写一份。
- **`internal/ui`**（CLI 的 TUI）：`model.go` 是 bubbletea 生命周期；工具事件归并在 `tool_events.go`、异步命令工厂在 `tea_commands.go`（与 `slash_commands.go` 的斜杠命令是两回事）；渲染分 `render.go`（组装）/ `render_approval.go` / `render_tools.go` / `markdown.go` / `format.go`，调色板集中在 `render.go`。包说明见 `doc.go`。

自检：`go build ./...`、`go test -race ./internal/... ./cmd/runcode/...`、`golangci-lint run ./...`。**lint 存量已清零，新增告警一律当回归处理**（不再有"既有基线"可推诿）。豁免只有两种合法形式：`.golangci.yml` 里按类别写明理由的排除（G104/G304/G301、测试排除），或单点 `//nolint:linter // 原因`。加新的豁免前先确认不是真问题。

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
- **按品牌打包用 `scripts/build-desktop.sh`**（在 `cmd/runcode-desktop` 下执行），它一次配齐品牌的四处开关——前端 `VITE_BRAND`、Go 窗口标题 `-ldflags`、`wails.json` 的应用名/产物名、`build/` 下的图标与 macOS `Info.plist`——构建完自动还原这些打包资产，工作区不留脏改动。手敲 `wails build` 只会改到前两处，成品会出现"界面是智开、bundle 标识符还是 XRUN"这类只在装机后才看得出的错配。
  ```bash
  ./scripts/build-desktop.sh --brand zhikai              # 当前平台
  ./scripts/build-desktop.sh --brand zhikai --universal --zip   # macOS 通用二进制 + 可分发 zip
  ./scripts/build-desktop.sh --test                      # 测试版（含上下文审核）
  ```
- **测试版构建（`--test`）**：注入 `internal/desktop.testBuild` 标记（`internal/desktop/testbuild.go`），解锁仅测试版的"上下文审核"——设置页多一个开关，开启后引擎每次发给模型的完整请求上下文（系统提示词、消息历史、工具清单）按会话落 JSONL 到 `<UserConfigDir>/runcode/context-audit/`，并起一个仅监听 127.0.0.1 的查看页（`internal/desktop/contextaudit*.go`，数据链路是引擎 `Options.LLMRequestObserver`）。前端不用 VITE 开关：设置区块按后端 `ContextAuditStatus().supported` 决定渲染，单一事实来源。正式分发包一律不带 `--test`，此时功能整体不存在（命令拒绝开启、观测器不接线）。

##### macOS 打包

必须在 Mac 上构建（同上，不能交叉编译）。`build/darwin/Info.plist` 是默认品牌的包清单；`build/brands/<品牌>/Info.plist` 覆盖它——**每个品牌的 `CFBundleIdentifier` 必须不同**（XRUN 是 `cn.ouconline.ai.xrun`，智开是 `cn.ouconline.ai.zhikai`），否则 macOS 把两个品牌当成同一个应用，偏好设置、通知授权与 Gatekeeper 记录会互相覆盖。品牌若没有 `build/brands/<品牌>/appicon.png` 就沿用 `build/appicon.png`，三平台同一张图标（智开当前如此）。

分发给他人需签名+公证，否则 Gatekeeper 拦截（自用可右键「打开」绕过）：设 `APPLE_SIGN_ID`（签名）与 `APPLE_KEYCHAIN_PROFILE`（公证）后脚本自动执行。`.app` 压缩必须用 `ditto -c -k --keepParent`，`zip` 不保留符号链接与权限位、会破坏签名。

**已知差异：macOS 上通行证令牌不落盘，每次启动都要重新登录。** 令牌加密存储只实现了 Windows 的 DPAPI（`internal/desktop/secret_windows.go`）；非 Windows 的 `secret_other.go` 返回 `ok=false`，`persistTokens` 因此不写文件——宁可不存，也不明文落盘。要消除这个差异得接 macOS Keychain。

#### 品牌（白标，`frontend/src/core/brand.ts`）

界面上的名字/标记/文案（标题栏、起始页、登录门、空对话引导）全部读 `BRAND`，不在组件里写死。多套品牌都留在 `brand.ts` 的 `BRANDS` 里，构建时选一套——**原品牌永远保留，不是被替换**。当前两套：`runcode`（XRUN，内置 X 矢量标，"AI 编程助手"）、`zhikai`（智开，`@/assets/zhikai-logo.png` 位图，"AI 办公助手"）。换品牌两种等效方式:构建前设 `VITE_BRAND=zhikai`，或改 `brand.ts` 的 `DEFAULT_BRAND` 一行;拼错/未知值一律回落默认品牌（`selectBrand` 有单测）。位图 < 4KB 被 Vite 内联成 data URI，产物自包含、运行期不联网。加品牌只改 `BRANDS`；新增图片走 `@/assets` 导入。

OS 窗口标题（无边框窗口下只在任务栏/alt-tab 显示，UI 标题是前端自绘）在 Go 侧 `main.go` 的 `brandTitle`，默认 `XRUN`，打包智开版时 `wails build -ldflags "-X main.brandTitle=智开"` 配合 `VITE_BRAND=zhikai`。`wails.json` 的 `outputfilename`（exe 名）是静态项，要改 exe 名单独改它。

#### 前端目录（`cmd/runcode-desktop/frontend/src`）

跨目录一律用 `@/` 别名（`vite.config.ts` 的 `resolve.alias` + `tsconfig.json` 的 `paths`），同目录内保持 `./` 相对路径——这样挪文件不牵动导入方。分层自下而上，依赖只朝下：

| 目录 | 职责 |
| --- | --- |
| `core/` | 后端通道与领域纯逻辑：`bridge`、生成的 `protocol/`、`paths`、`format`、`tool-catalog`（内置工具中文目录）、`custom-models`、`passport-account`、`brand`（白标品牌：名字/标记/文案，构建时选一套） |
| `ui/` | 与业务无关的通用件：`tokens`（BTN/DRAG 等类名常量）、`feedback`（**错误/警告的唯一出口**，见下）、`layout`（`PageShell` 管理页骨架 / `Placeholder` 空态与加载态 / `InsetRow`+`INSET_BOX` 设置行）、`keys`（`isComposingKey`：输入法组字判定，凡是把 Enter 当快捷键的地方都要先过它）、`icons`、`markdown`、`popover`、`fields`、`model-picker`、`badges`、`glyphs`、`toggle`、`confirm-dialog`… |
| `hooks/` | 跨模块通用钩子：`use-stick-to-bottom`、`use-persistent-state` |
| `chat/` | 对话渲染层：`blocks`（分组/合并纯逻辑）、`tool-text`（工具事件→中文，纯函数）+ 各类卡片组件 |
| `preview/` | 预览：`classify`/`tabs`（纯逻辑）、`file-panel`/`diff-panel`/`pane`/`file-browser`，Office 查看器在 `viewers/` |
| `composer/` | 输入区：`keymap`（按键归属：输入法组字 > 候选框 > 发送/换行，纯函数）、`mention`（触发解析与候选排序，纯函数）、`mention-picker`、`toolbar`、`index` |
| `pages/` | 整页：`plugins/`、`permissions`、`mcp`、`memory`、`start/`、`settings/` |
| `session/` | 应用状态与副作用钩子：`use-conversation`（引擎事件订阅在此）、`use-session`、`use-plan`（阶段化计划模式的运行状态与审批草稿）、`use-permission-queue`、`use-preview-panel`、`use-workspace-files`、`use-auto-preview`、`use-toast` |
| `shell/` | 外壳组件：`title-bar`、`status-bar`、`chat-pane`、`preview-side`、`permission-modal`、`sidebar` |
| `dev/` | 预览页，不进正常流程：`?preview=ui` 公共组件画廊（**改样式后的视觉回归就看它**——自动化检查证明不了观感）、`?preview=tools` 工具卡、`?preview=thinking` 思考面板 |

`App.tsx` 只负责把上述钩子接起来并按视图摆放 shell 组件，不放具体逻辑。改行为找 `session/`，改样子找 `shell/` 或对应页面。

**公共组件与设计 token 的完整说明在 [`frontend/src/ui/README.md`](cmd/runcode-desktop/frontend/src/ui/README.md)**——token 对照表、每个组件的用法与**何时不该用**、以及每条约束各自是从哪次真实事故来的。写前端前先看它，改 `ui/` 下的东西请同步它。铁律五条：

1. **颜色 / 圆角 / 阴影 / 字号只用 token**，不写 `#hex`、`rgba()`、`rounded-[8px]`、`text-[12.5px]` 这类字面值（三类例外见文档）。透明度用斜杠语法 `border-red/35`。
2. **字号只有整数 px，没有半档**（`9 10 11 12 13 14 15 16 18 20 22 24 26`，主力 12/13）。要拉层次跨整档——0.5px 在本应用的 DPI 下看不出来，只会变成下一个人的困惑。
3. **报错与警告只从 `ui/feedback` 出**：对话流用 `SystemNote`（居中分割线条，**不是气泡**——只有模型说的话才进助手列 `BotRow`），弹窗表单用 `Banner`，页面级失败用 `InlineError`。严重度只有 `Tone` 一个维度，它同时决定颜色与字号，调用点别另挑字号。警告文字用 `text-amberink` 不是 `text-amber`（后者对比度约 2.3:1，小字不达标）。
4. **管理页用 `PageShell`**（mcp / memory / settings）。plugins 与 permissions 有各自的固定页头与更宽版心，不套这层。
5. **把 Enter / 方向键当快捷键前先过 `isComposingKey`**，否则中日韩输入法组字时会被抢键。

前端自检（在 `frontend/` 下）：`npm run typecheck`、`npm run lint`、`npx vitest run`、`npm run build`。**`npm run lint` 不是可选项**——`tsc` 证明不了 `useEffect` 少列依赖，`react-hooks/exhaustive-deps` 才管这件事，`session/` 下的钩子全靠它兜底。要豁免必须写 `// eslint-disable-next-line` 并在上方注明为什么（现有两处：通行证协调器只建一次、起始页自动进入只评估一次）。纯逻辑模块（`chat/tool-text`、`chat/blocks`、`chat/plan-draft`（审批区清单的增删/排序/整理）、`preview/classify`、`preview/tabs`、`composer/mention`、`composer/keymap`、`ui/keys`、`ui/model-picker`（`toModelOptions`：平台+自定义候选合并，输入框与设置页共用）、`pages/mcp-draft`、`core/custom-models`、`core/passport-account`、`core/brand`）都有单测，新增纯函数请一并补测。
