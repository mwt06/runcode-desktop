# CLAUDE.md

## 协作原则

- 不要机械追求“最小改动”。实现方案应优先保证系统的可靠稳定、可扩展、性能可控、高内聚、低耦合。
- 对简单 bug 或明确小需求，可以保持小范围修改；但如果“最小补丁”会造成脆弱设计、重复逻辑、抽象泄漏、后续难扩展或性能隐患，应主动提出并实现更稳健的基础层。
- 规划功能时要说明取舍：哪些是当前必须做的可靠性/扩展性基础，哪些是暂不做的过度工程。
- 默认避免无关扩张，但不要为了少改几行牺牲长期架构质量。

## 构建与打包

引擎已独立成仓：**`gitlab.ouc-online.com.cn/aibase/agentloop`**（require 固定 tag，无 replace）。本地开发用同级 checkout `../agentloop` 经 `go.work` 联动（改引擎实时生效）；`GOWORK=off` 构建则设 `GOPRIVATE=gitlab.ouc-online.com.cn` 直连内网 GitLab 拉取 tag。改引擎（ReAct 循环、工具、权限、provider、持久化等）去 agentloop 仓库；本仓库只改外壳。

本仓库含**三个 Go module**（依赖方向：外壳 → agentloop，反向不存在）：

1. **根模块（`github.com/wt68/runcode`）**——CLI/TUI（`cmd/runcode`、`internal/ui`、`internal/command`）+ 桌面核心（`internal/desktop`、`internal/protocol`）+ 桌面专属 host 工具（`internal/previewtool` = `open_preview`、`internal/officetool` = `ReadOffice`、`internal/plantool` = `plan_write`、`internal/runtimetool` = `install_runtime`（缺 Python/Node/Git 时模型自己装，走的就是设置页那个安装器；归类 `ClassMutating`，每次都要用户批准），均经 `engine.Options.ExtraTools` 只在桌面注册；`internal/skilltool` 与 `internal/websearchtool` 走另一条路——经 `engine.Options.SkillTool` / `engine.Options.WebSearchTool` **替换**内置的 Skill / WebSearch 工具，因为会话内工具名唯一，同名工具只能换不能加。WebSearch 换掉的是引擎那个 DuckDuckGo 抓页搜索，改走平台自己的联网搜索：经 Bridge 的 `/v1/websearch` 调 AI.Core，带登录用户的通行证令牌与选定租户，模型默认 `al-websearch`（`RUNCODE_WEBSEARCH_MODEL` 可覆盖）；只有通行证连接装它，自填端点/自定义模型保留内置的那条——接线见 `internal/desktop/websearch.go`）+ `tools/protogen` 与 `tools/runtimepacks`（把上游的 Python/Node/Git 绿色包重打成本应用的运行时包，产出清单供 Bridge 下发）。
2. **桌面外壳（`cmd/runcode-desktop`，嵌套 module）**——Wails/CGO 重依赖隔离层。
3. **服务端骨架（`cmd/runcode-server`，嵌套 module）**——独立仓库服务端的可跑参考实现。

`go.work` 已提交（use：`.`、`./cmd/runcode-desktop`、`./cmd/runcode-server`、`../agentloop`）供本地联动；**CI/发布链路一律 `GOWORK=off`**。本机正式构建经 `GOPRIVATE` 从内网 GitLab 解析固定 tag；GitHub CI 经 `.github/actions/setup-engine` 用 `ENGINE_REPO_TOKEN` checkout `mwt06/agentloop` 的同一 tag，再向三个 module 注入仅 CI 使用的临时 replace。该 action 在 checkout/replace 前用 `check-version.sh` 核验三模块 require 一致，不能靠 workspace 或临时 replace 掩盖版本漂移。引擎改动须发布真实 tag 并同时升三个 require 才进正式发布。

**本地引擎测试打包与 tidy**：`go mod tidy` 不使用 `go.work`，引擎新增未发布包（如 `imageinput`）时会错误地去固定 tag 找包。品牌脚本的 `--local-engine` 会先验证引擎是工作区 main module，再向 Taskfile 传 `LOCAL_ENGINE=1`，仅此模式跳过 tidy；正式构建仍传 0 并执行 tidy，不能靠临时 replace 掩盖依赖漂移。

### 引擎升级接入契约

- CLI/TUI、desktop、server 均启用 `Options.OmitRequestSnapshots`：不在回合结果保留完整请求历史；上下文审核仍走 `LLMRequestObserver`，不依赖这些结果快照。
- 宿主工具通过 `tool.InputPresentationProvider` 声明流式主参数（文件路径、运行时名称、计划阶段）；这仅控制展示，不改变 schema、授权或执行，更不是敏感参数隐藏机制。
- 引擎 `host.Manager.Close` 先取消并等待构建/回合（含收尾回调）和状态落盘操作，再关资源；失败保留关闭中的会话供重试，不接受新回合或同 ID 重建。`OnTurnEnd` 不能同步关闭自身会话，否则会等待自己。desktop 关闭失败必须向上传错，不标已关闭、不丢编辑复审/预览引用；TUI 退出先停止事件发送、取消并等待回合，再用独立有界 context 清理。不能复用已取消的回合 context 做清理。
- **2026-09-30 已随引擎 `v0.15.0` 发布**：包含上述 API/关闭修复、上下文预算、提问分支、图片路由与后台无控制台工具。GitLab 与 GitHub 镜像使用同一 tag，三个 module 统一 require `v0.15.0`。正式构建使用 `GOWORK=off`；仅开发联调显式 `--local-engine`，不能拿工作区验证替代固定 tag 验证。

### 上下文自动压缩契约

- 主请求前按完整请求的校准估算检查预算，达到 **80%** 自动压缩，目标约 **60%**；低于 80% 才继续原回合，不重跑已完成工具。失败/不收敛明确报错，不用仅回收少量 tokens 的提示冒充压缩成功。输出上限也受剩余窗口约束。
- **思考原文留在完整历史，且必须参与摘要**；摘要保留理解、判断依据、结论、不确定性和未完成事项，近期思考在预算允许时保留原文。不能只取 TextContent 而漏掉 thinking；原生 reasoning 是否能回传仍取决于 provider，不在这次压缩改动中假装补齐。
- `context:compaction` 由引擎 host 发出开始/完成/失败事件，前端按 sessionId 更新，自动压缩期间不清 busy，摘要重试不清主回答。协议经 protogen 生成。以上已包含在固定引擎 `v0.15.0`，正式打包不需要 `--local-engine`。

### 常用命令（根目录执行）

- 全量编译：`go build ./... ; go -C cmd/runcode-server build ./... ; go -C cmd/runcode-desktop build ./...`（或 `make build`）。
- 测试（CI 用 `-race`，三平台）：`go test -race ./... ; go -C cmd/runcode-server test -race ./...`（或 `make test`）。
- Lint：`golangci-lint run`（或 `make lint`；配置根 `.golangci.yml`，启用 gosec/errcheck/gocritic）。
- 协议 TS 再生成（引擎 protocol 变更后）：`go run ./tools/protogen`；CI 用 `--check` 防漂移。
- 打运行时包（Python/Node/Git 的绿色包，传 OBS 用）：`go run ./tools/runtimepacks --list` 先看要下什么，再去掉 `--list` 实打；见 `tools/runtimepacks/main.go` 的说明。
  运行时清单有两条路：默认走 Bridge 的 `/api/app/runtimes`；打包时加 `--runtime-manifest 'https://<obs>/runtimes-{platform}.json'` 则改走对象存储上的静态文件（`{platform}` 由客户端换成 `windows-amd64` 这种，见 `runtimeManifestURL`），**不需要服务端**，代价是换版本要重传 JSON。
- 出 CLI 二进制：`go build -o runcode.exe ./cmd/runcode`。

### `internal/` 文件分工

按功能模块组织；Wails 入口保留在 `desktop.App`，能独立拥有状态与流程的能力下沉为独立包，依赖只朝下：

- **`internal/protocol`**（桌面自己的 wire 类型：设置表单、通行证、技能/子代理/MCP/工具管理页、编辑复审、harm 提示）。**加字段、加 DTO 请加在这里，不要加进引擎**——引擎的 `agentloop/protocol` 只负责"跑一个回合"的契约（assistant delta / 工具事件 / 审批 / 回合结果 / 会话状态 / 错误 / envelope），且与 `cmd/runcode-server` 共享。判据是"谁产生它"：引擎 `host` 包产生或消费的归引擎，只有本外壳用的归这里，**没有例外**。命令清单 `CommandKinds` 就在这里（`internal/protocol/commands.go`）——**新增一个 Wails 命令只改本仓**，引擎不必发版；引擎只保留分类词汇 `protocol.CommandKind` 与三个常量（"query 意味着什么"两端必须一致，"有哪些命令"各自声明，`cmd/runcode-server` 另有自己的一份）。两个包由 `tools/protogen` 合并生成同一份 TS，重名会直接报错。
- **`internal/desktop`**（桌面核心，Wails 把 `App` 的导出方法绑给前端，所以命令仍挂在同一个类型上；这只约束绑定入口，不妨碍实现委托给独立服务）：`app.go` 只留 App 结构与会话开关；回合在 `turn.go`、自动标题在 `title.go`、运行中可变的会话设置在 `session_settings.go`；其余按功能各自成文件（`skills` / `agents` / `mcp` / `passport` / `oauth` / `tokens` / `preview` / `editstore` / `plan`（阶段化计划模式的阶段机与审批闸门）/ `custommodels` / `config` / `store` / `disabled` / `harm` / `appdirs`（本应用自己的安装/数据目录不再走"项目外授权"，包一层 `permissions.Policy`）/ `update`（更新命令门面与宿主装配，完整状态机/流程在 `internal/appupdate`；平台安装器仍留本包，通过包含文件、版本、SHA256 的不可变请求接入；`version.go` 是版本号与产品标识，两者都由打包脚本经 `-ldflags` 注入）/ `download`（到 `internal/download` 的薄适配，更新、技能市场与运行时包复用同一份带进度与 sha256 的有界下载实现） / `runtimes`（运行时环境：Python/Node/Git 的绿色包，查清单 → 下载 → 校验 → 解压到用户目录，**全程不提权**；`runtimepack.go` 是"本机知识"——包内布局与系统探测，故意不来自服务端清单；`runtimeenv.go` 把它接进 PATH 与 `SystemPromptAppend`，两者缺一不可：只给 PATH 模型不知道 Python 在那儿，还会回一句"请先安装 Python"。**PATH 只写进本进程、不放进 `engine.Config.ToolEnv`**：ToolEnv 是开对话那一刻的快照，放进去就把这个对话的 PATH 冻住了，对话中途装上的运行时在这个对话里永远找不到；引擎的 Bash 每条命令都现读 `os.Environ()`，所以写进程 PATH 就够。模型那条安装路在 `runtimetool.go`（清单没取就先取、设置页正在装就等、装好没放行就补授权；`runtimeResolver` 给审批弹窗配"装什么、多大"那句话——引擎的审批请求里不带工具参数）；`untargz.go` 是它专用的解压器——运行时包用 tar.gz 而非技能包那种 zip，因为里面有符号链接与可执行位） / `privilege` + `askpass`（让模型用 sudo，**两道人工关**：先在应用里审批那条命令——永不记住、永不被裁判自动放行——再在应用自己的弹框里输系统密码，模型永远看不到。askpass 就是本应用二进制自身（`IsAskpass`/`RunAskpass`，`main.go` 与 `main_kylin.go` 都要在单实例锁之前处理掉）；服务端对每个连接核验来者：同一用户、是本应用二进制、**父进程名为 sudo 且有效 UID 为 0**——只看名字会被一个叫 sudo 的脚本骗过（麒麟真机实测）。Linux 要求 `DISPLAY` 非空（sudo 自动改走 askpass 的前提）；macOS 复用 Unix 密码通道，用 `LOCAL_PEERPID`/`LOCAL_PEERCRED` + libproc 核验本应用及有效 UID 为 0 的系统 `/usr/bin/sudo`（`askpass_darwin.go`，桌面 cgo 构建开放，无 cgo 构建关闭），模型显式用 `sudo -A -k`，不伪造 DISPLAY。Windows 走原生 sudo/UAC（Windows 11 24H2+，须用户自己在系统设置启用），只读系统/组织策略，不自动改安全设置；新窗口模式不可假定已拿到输出或退出码（`privilege_windows.go`）。sudo 下的 rm/dd/mkfs 等仍然硬拒，pkexec/doas 一律拒——引擎的特权名单漏了它们，pkexec 会绕开应用内审批。端到端测试 `askpass_e2e_test.go` 带构建标记，要在真 Linux/macOS 上用真 sudo 跑；不需要密码的冒充请求测试在 `askpass_transport_test.go`）/ …）。技能与子代理共用的作用域目录解析与命名规则在 `resources.go`（`resourceRoot(kindSkills|kindAgents, scope)`），别再各写一份。
- **`internal/ui`**（CLI 的 TUI）：`model.go` 是 bubbletea 生命周期；工具事件归并在 `tool_events.go`、异步命令工厂在 `tea_commands.go`（与 `slash_commands.go` 的斜杠命令是两回事）；渲染分 `render.go`（组装）/ `render_approval.go` / `render_tools.go` / `markdown.go` / `format.go`，调色板集中在 `render.go`。包说明见 `doc.go`。

### 功能边界与配置契约

- **`internal/appupdate`**：更新服务拥有检查、下载/校验、取消、安装调度、缓存与上次安装结果的状态；通过 `Options` 注入网络、缓存、令牌来源、平台安装/打开文件夹/退出及事件出口，不接收 `*desktop.App`。`deps_test.go` 用 `go list -deps` 守住传递依赖边界。版本比较也在此，桌面版本号/产品标识的 ldflags 路径不变。
- **`internal/download`**：共用下载基础设施，不依赖 desktop 或更新服务；下载请求不带账号凭据，超时/取消和体积上限由调用方指定。
- **配置四种角色分开**：协议 `StartSessionRequest` 只管启动；`SaveSettingsRequest` 只管设置页拥有的字段；`SettingsView` 是脱敏读取视图（`LoadConfig` 返回它）；`desktop/desktop_config.go` 的私有 `desktopConfig` 兼容旧 `desktop.json`，密文凭据和模型存储不得回到公共请求里。保存设置不再接受连接/模型/租户/思考强度快照，它们各走专用命令。
- 所有配置读改写经过 `store.go` 的 `configMu` + `writeConfigHeld` 原子写；`SaveSettings` 一次更新并返回持久化失败。启动保存从旧配置出发，仅改启动字段，不为每个新应用设置补一条“沿用旧值”。
- 会话/权限/OA 的装配顺序不因拆包改变；本轮不拆账号/会话协调、不引入 DI 容器或通用事件总线。

自检：`go build ./...`、`go test -race ./internal/... ./cmd/runcode/...`、`golangci-lint run ./...`。**lint 存量已清零，新增告警一律当回归处理**（不再有"既有基线"可推诿）。豁免只有两种合法形式：`.golangci.yml` 里按类别写明理由的排除（G104/G304/G301、测试排除），或单点 `//nolint:linter // 原因`。加新的豁免前先确认不是真问题。

### 桌面版（Wails，`cmd/runcode-desktop`）

- 桌面是**嵌套 Go module**（`cmd/runcode-desktop/go.mod`），用 `replace github.com/wt68/runcode => ../..` 指回核心，把 Wails/CGO/WebView 重依赖隔离在核心之外；核心的 `go build ./...` 与 CI 不会拉 Wails。模块路径仍在 `github.com/wt68/runcode/...` 下，故可复用核心的 `internal/` 包。
- **正式打包**（产出可发布 exe，需已装 `wails` CLI，本机验证 v2.12.0；会跑 `npm install` + `npm run build` 重建前端）：
  ```bash
  cd cmd/runcode-desktop && wails build
  ```
  产物：`cmd/runcode-desktop/build/bin/XRUN.exe`（应用名/输出名 `XRUN` 来自 `wails.json` 的 `outputfilename`）。
- **仅 Go 侧快速编译检查**（不打包、不重建前端）：`go -C cmd/runcode-desktop build ./...`。
- 跨平台：Wails **不能交叉编译**（各 OS WebView 不同——Windows WebView2 / Linux WebKitGTK / macOS WKWebView），需在目标平台原生构建。CI 见 `.github/workflows/desktop.yml`，八个目标：Windows / macOS / 麒麟 V11 / 麒麟 V10，各两个架构。

##### 两份外壳：v3 与 v2（麒麟 V10）

`cmd/runcode-desktop` 里有**两个 main**，靠 `kylin` 构建标记二选一。业务逻辑、引擎与整个前端共用（v2 与 v3 的运行时差异由一个构建期垫片吸收，见下），Go 侧只差这一个文件：

| | `main.go`（`!kylin`） | `main_kylin.go`（`kylin`） |
| --- | --- | --- |
| Wails | v3 | v2 |
| WebKitGTK | 4.1 / 6.0 | **4.0** |
| 平台 | Windows、macOS、麒麟 V11 | 麒麟 V10 |
| 窗口 | 双窗口（主窗 + 录音窗） | 单窗口 |

为什么非得分两份：**麒麟 V10 的 WebKitGTK 只有 4.0 这个 ABI**（任何 V10 套件里都没有 libsoup3，装不上 4.1），而 Wails v3 只有 4.1 与 6.0 两条路径，连它的 `gtk3` 老路走的也是 4.1；v3 还调用了 `webkit_web_view_evaluate_javascript`，那是 WebKitGTK 2.40 才引入的。Wails v2 相反：默认就编到 `webkit2gtk-4.0`，用到的二十个 WebKit 函数全是老接口。V10 与 V11 因此**不可能是同一个二进制**。

几条实测出来的纪律：

- 品牌变量（`brandTitle` / `brandID`）住在**不带标记**的 `brand.go`，两份外壳共用。放回 `main.go` 的表现是另一份编译时报 undefined。
- `go.mod` 同时 require v2 与 v3，无依赖冲突；`go mod tidy` 会保留 v2（自定义标记的文件它算得进去），只有被编进去的那套会进二进制。
- v2 那条**整条链路不碰 wails3**：它的 CLI 自己就要 webkit2gtk-4.1，在 V10 的编译环境里装都装不上。`--kylin` 走 vite 构建 + `go build -tags kylin,desktop,production` + nfpm。
- 编译机的 glibc 必须**不比目标新**（glibc 只向后兼容）。V10 是 2.31 → 只能在 `ubuntu:20.04` 容器里编（也是唯一还带 `libwebkit2gtk-4.0-dev` 的 Ubuntu）；V11 是 2.38 → `ubuntu-22.04`（2.35），不能用 24.04（2.39）。
- Linux 上应用名自动换成 ASCII 的产品标识：它同时是 deb 包名、可执行文件名与 `.desktop` 文件名，而 deb 包名规范不接受中文。中文走 `.desktop` 的 `Name` 字段。
- **`webkit2_41` 是 v2 时代的标记，v3 里不存在**，脚本里那个已清掉。v3 的老路径开关叫 `gtk3`，且在 v3.1 会被移除。
- **`-tags kylin` 一个不够，要 `kylin,desktop,production`**。v2 按 tag 选 `internal/app` 的实现：漏了 `production` 编进去的是 `app_default_unix.go` 那个占位版，`CreateApp` 直接返回 "Wails applications will not build without the correct build tags." 然后退出码 1。成品能编译、能装、双击**没反应**，只有从终端跑才看得见那行。这条链路不经过 wails 的 CLI，CLI 默认会带的 tag 就得在打包脚本里手工补齐。验证手法：`go list -tags ... -f '{{.GoFiles}}' github.com/wailsapp/wails/v2/internal/app` 看选中的是哪个 `app_*.go`。
- **前端是按 Wails v3 的运行时写的**（`Call.ByName` / `Events.On` 来自 `@wailsio/runtime`，底层要 `/wails/runtime` 与 `window._wails`），v2 一样都不提供——它注入的是 `window.go` 与 `window.runtime`。`--kylin` 因此设 `VITE_WAILS=v2`，由 `vite.config.ts` 把**模块名** `@wailsio/runtime` 别名到 `frontend/src/core/wails-v2-runtime.ts` 的垫片上。**接缝只有这一处**：五个调用点与 protogen 的模板都不知道这件事，v3 产物也完全不受影响（别名不存在时垫片不进包）。不要改成运行时探测 `window._wails`——那的失败模式是时序相关的偶发。垫片的导出覆盖率由 `wails-v2-runtime.test.ts` 扫源码卡住，新用一个 v3 API 时红的是 CI 而不是麒麟机器上的白屏。代价是 V10 上**拖文件进输入框没有**（v2 的 file drop 是 `--wails-drop-target` + `OnFileDrop` 另一套），粘贴与选文件不受影响。
- **deb 的 `Architecture` 别信 nfpm 的默认值**。`nfpm.yaml` 写的是 `arch: ${GOARCH}`，可两条 Linux 链路都没人把 `GOARCH` 喂给打包那一步（v3 那条只设在 `build:native` 的 env 里，打包走的是另一个 task），取空时 nfpm 兜底成**硬编码的 amd64**。表现是 arm64 的包里装着 arm64 的二进制、control 却写 amd64，`dpkg -i` 一句「体系结构不符」，而看文件名完全看不出来。打包脚本现在就地把真实架构写死进去，不走环境变量。
- **检查更新时 V10 上报的平台是 `kylin10/<架构>`，不是 `linux/<架构>`**（`update_platform_kylin.go`，按 `kylin` 标记选）。V10 与 V11 的 GOOS/GOARCH 相同却装不了同一个包，平台键只到 `linux/arm64` 的话 Bridge 上那一格只能放一边的包，另一边下载、校验全绿、最后装不上。1.0.17 及以前的 V10 仍报 `linux/<架构>`，那一格要等它们升完才能改放 V11 的包。
- **Linux 上「立即安装并重启」由应用自己跑 `sudo -A apt-get install <已校验的 deb>`**（`update_linux.go`，1.0.19 起）：走应用的密码框（与运行时包加白同一套 `app:` 键上膛），交给 apt 前**再验一遍 sha256**（包躺在用户可写的缓存目录里，接下来要以 root 执行它），装完起一个 `setsid sh` 等本进程退出再 exec 新二进制（单实例锁在旧进程手里，抢先起来的新版会自己退出）。只有**归 dpkg 管、包名等于产品标识**的二进制才开放；开发构建退回「打开安装包所在文件夹」。下好的包必须保住 `.deb` 扩展名：曾被存成 `.zip`，麒麟上双击打开的是压缩包管理器。**麒麟的「应用程序来源检查」只认麒麟根证书或国内商业 CA 的 SM2 证书签的包**（`/usr/share/ca-certificates/kylin/`），`kylinsigntool` 签名时就校验证书链，自签证书（RSA、SM2、自建 CA 都试过）连签都签不上。

**麒麟适配认证的包规范检查（2026-09-20 用官方《麒麟兼容性适配工具》V4.4 在 V10 SP1 2403 真机上逐条跑出来的）**：认证要过的是 28 条 `common-standards-*`，四条踩在我们身上，前两条判「不通过」、整份规范性检查因此为不通过：

- **deb 的文件名必须等于 `{Package}_{Version}_{Architecture}.deb`**（`common-standards-01`）。nfpm 出厂时给的就是这个名，是 CI 的「整理产物」那步为了区分 V10/V11 把它改成了 `zhikai-1.0.11-kylin10-arm64.deb` —— 一改就是一条不通过。现在 deb 保留原名，V10/V11 的区分由 artifact 名承担（下载下来的压缩包本来就带 flavor）。V11 那条的 wails3 按 `-name` 起名（`zhikai.deb`），打包脚本的 `normalize_deb_names` 按包里的 control 改回规范名。**GitHub Release 是例外**：它是八个目标共用的平铺命名空间，V10 与 V11 的规范名一模一样，原样挂上去后传的静默覆盖先传的（1.0.17 的 V11 arm64 包就这么丢了），所以挂 Release 那份改叫 `zhikai-<版本>-<kylin10|linux>-<架构>.deb`。**送认证用 artifact 里的包**，或把 Release 上下的改回规范名。
- **`.desktop` 必须有 `Name[zh_CN]`**（`common-standards-17`）。v2 那条是脚本里的 heredoc，v3 那条由 `wails3 generate .desktop` 生成、它没有这个开关，所以在 `build/linux/Taskfile.yml` 里生成完补一行。
- **可执行文件要装在 `/opt/apps/<包名>/`**（`common-standards-27`，判警告）。原来在 `/usr/local/bin`。`/opt/apps` 不在 PATH 上，所以 `.desktop` 的 `Exec` 必须写全路径，两份外壳都要改。askpass 那条链不受影响——它比的是 `os.SameFile` 而不是路径字面量。
- **图标要么给 scalable 的 svg，要么 16/24/32/48/64/96/128/256/512 九档 png 全给齐**（`common-standards-22`，判警告）。原来只有一张 128。`tools/icongen`（纯 stdlib，面积平均缩放）在打包前从 `build/appicon.png` 现生成九档，产物在 `build/linux/icons/`，不进版本库；必须在品牌图标覆盖 `build/appicon.png` **之后**跑。

跑法（工具本身不用装）：它是纯 shell + python，一个 ELF 都没有，所以 KYSEC 执行控制管不着，`dpkg-deb -x` 解出来就能用；`src/scripts/kylindebcheck/run.sh software <deb>` 是**纯静态**检查，不装包、不用 root、十几秒出结果，改完打包配置想验证就跑它。整套 `KAT-cli.py` 还会装/卸被测包做运行时检查，那条要 root 且会被 KYSEC 的「应用来源检查」弹框拦住。它的 GUI 在 2403 上起不来（依赖 PySide2 5.14，那版要 Qt 5.12.8 ABI，而 2403 是 5.15.13），只能走手册 FAQ Q2 的命令行模式；命令行只缺一个 `loguru`，解个 wheel 到入口同级目录即可。

**KYSEC 执行控制（麒麟 V10 SP1 真机查实，影响一切"在麒麟上执行我们自己放下去的文件"的功能）**：经 dpkg 装的文件自动标为 `verified`（`sudo kysec_get <文件>` 可查），我们自己写出/解压出的 ELF 是 `unknown`，**执行会被拒**（`权限不够`，退出码 126；所谓 warning 模式实际是桌面弹一个 30 秒的确认框，没人点就拒）。它**连 `.so` 的加载也管**：可信的解释器去加载一个 unknown 的扩展模块同样失败（`failed to map segment from shared object`）。`sudo kysec_set -n exectl -v verified <文件>` 可以加白。推论：askpass 必须指向 deb 装的应用本体而不是临时脚本。弹框的实际规则（逐条实测）：**只在启动进程时弹**，允许之后该进程加载的 unknown `.so` 一路放行；**允许不会被记住**，下次启动照弹；而 verified 的进程去加载 unknown `.so` 会被拒——所以只给解释器加白不够。运行时包因此在装完后用一次 `sudo -A kysec_set` 把包里**全部 ELF**（按文件头认，不按扩展名）加白，走应用的密码框、带用途说明（`kysec.go` / `kysec_linux.go`，`getstatus` 不用 root 就能判断执行控制开没开）；加白时记下这批 ELF 的指纹，之后 pip 装了带 C 扩展的新库、指纹一变就重新亮出「授权」按钮。

**模型 pip 装完新库立刻请人放行**（`trustwatch.go`）：光靠"下次开应用时复核"不够——中间那段时间里用户看到的是脚本莫名其妙失败。三个钩子：每条 Bash 命令跑完（`hostSinkAdapter` 认 `ToolEvent` 的 completed）在后台查一次，有新的未放行 ELF 就弹密码框，框上写明是哪个库带来的（`newlyAddedNames` 按比加白标记新的 mtime 猜库名）；模型的**下一条** Bash 在 `runtimeResolver.Resolve` 里同步走一遍同一条检查（`ensureRuntimeTrust`），既清上次遗留的欠账，又顺带排队——否则用户还在输密码它已经去 import、撞上 "failed to map segment" 然后开始改脚本；**启动后 8 秒**再补一次（与自动检查更新同一个延迟，且必须延迟：密码框是前端画的，头几秒它还没订阅事件，这时发出去的请求没人接，超时会被记成"用户拒绝"）。三条边界：检查要便宜（先比对 site-packages / node_modules / bin 三个目录的 mtime，变了才做那趟六千文件的扫描）、**拒绝要被记住**（关掉密码框后同一批指纹不再自动弹，否则每条命令弹一次比不做还糟；用户自己点「授权」会清掉这个记号）、执行控制没开就整条跳过。真正调安全中心那一步是变量 `trustNow`，测试里换掉它，所以这条链在没有图形会话的机器上也能测。

**WebKitGTK 版本**：V10 基础版是 2.28.1，`10.1-2403-updates` 源里是 2.38.6。真机（V10 SP1 arm64，打过 2403 更新）是 **2.38.6，已跑通**——它支持 `@layer` 与 `color-mix()`，现有的 Tailwind v4 前端只损失 `@property`（被 `@supports` 包着，表现是渐变等效果打折）。停在 2.28.1 的机器**仍未验证**，大概率需要额外的样式降级；判定方法是在目标机器上跑 `apt policy libwebkit2gtk-4.0-37`。

**系统缩放 ≠ 100% 时字大框小**（同一台真机，2160×1440、150%）：WebKitGTK 建 WebView 时把 `Xft.dpi / 96` 设成**文字缩放**，只放大字、不放大布局，于是按钮字折行、侧栏标题只剩三四个字。Chromium 系与 WebView2 都是整页放大，只有它这样。`zoom_kylin.go`（cgo）在 GTK 主线程上把它换成整页缩放、文字缩放归 1；`RUNCODE_WEBVIEW_ZOOM` 可覆盖倍数。连带要改的是 v2 运行时的无边框拉伸：它拿 `window.outerWidth`（不随页面缩放换算）和 `clientX`（CSS 像素）比，右/下边会失灵，垫片里让 outer 跟随 inner。V11（v3、WebKitGTK 4.1）大概率同病，未验证、未改。

**麒麟 V10 的 Wayland 会话固定走 XWayland**（`main_kylin.go`，2026-09-20 在 V10 SP1 2403 Wayland 会话上实测）。两个都是只在真机上才看得见的毛病：

- **双标题栏**：窗口是无边框的、GTK 也确实声明了 `_MOTIF_WM_HINTS` 的 decorations 位为 0，可 UKUI 的 `kwin_wayland` 还是会在应用自绘的标题栏之上再画一条系统标题栏（图标是个通用齿轮）。换到 XWayland 就只有一条。
- **系统缩放被整个忽略**：GTK 的 Wayland 后端不读 `Xft.dpi`（那是 X 的资源库），`gdk_screen_get_resolution` 返回 96，于是上面那套整页放大算出来的倍数恒为 1，150% 缩放对这个应用完全不生效。走 XWayland 才拿得到 `Xft.dpi=144`。注意 `gsettings get org.ukui.font-rendering dpi` 是 96，别拿它当判据。

守卫两条：用户显式设过 `GDK_BACKEND` 不覆盖；`DISPLAY` 为空（没有 XWayland）不强指，宁可留着双标题栏也不能让应用起不来。纯 X11 会话没有 `WAYLAND_DISPLAY`，不受影响。**与文件里那条"单独设 GDK_BACKEND=x11 对白屏无效"不矛盾**：白屏要靠 `WEBKIT_DISABLE_COMPOSITING_MODE=1`，这里解决的是另外两件事。

**整页放大之后窗口尺寸也要跟着放大**（`runcode_scale_window`）。窗口里放得下的 CSS 像素是「物理像素 / 倍数」，而 Wails 的 `MinWidth/MinHeight` 是物理像素，拦不住。真机表现：1280×820 的窗口在 1.5 倍下只剩 853 CSS 像素宽，低于界面 1024 的设计下限，**一打开预览面板中间对话栏就窄到每行只剩一两个字、输入框被挤没**；拉大窗口立刻恢复。所以按同一个倍数把默认尺寸与最小尺寸一起放大，并夹到显示器可用区域内（否则窗口比屏幕还大、标题栏被顶出去拖不回来）。四个设计尺寸是 `main_kylin.go` 的 `winWidth/winHeight/winMinWidth/winMinHeight`，传进 cgo 而不是在 C 里写死，改尺寸只有一处要动。

**预览栏的宽度要在每次窗口变化时重新夹一遍**，不能只在挂载那一次。上限是「窗口宽 − 侧栏 268 − 对话栏下限 480」（`previewWidthBounds`），而窗口宽是随时会变的：整页缩放下把窗口调小，CSS 视口跟着缩，面板宽度却还留着启动时那个值，对话栏就又被挤到每行一两个字。`use-preview-panel.ts` 挂一个 `resize` 监听重新夹，并用 `wanted` 记住用户拖到过的宽度——只按当前窗口夹的话，缩一次窗口就把偏好永久改小了。

**应用对 SIGTERM 是正常响应的**（实测 ~540 ms 退出，Wails v2 接管了 SIGTERM/SIGINT 并调 `gtk_main_quit`），别被"发了 TERM 没死"骗到——它的信号 goroutine 只 `<-signalChannel` 一次，已经收过一次信号、退出流程卡住的进程不会再响应第二次 TERM。

**Windows ARM64 的 GUI 子系统**：`-H windowsgui` 在 Go → MSVC clang → lld-link 链上曾产出 Subsystem=3 的控制台 EXE；`.github/ccshim` 把 `-mwindows` / `-mconsole` 明确转为链接器 `/subsystem:windows` / `/subsystem:console`，不改有意的控制台程序。CI 用 `scripts/verify-windows-executable.py` 读取最终 PE，两个架构的桌面主程序都必须是 GUI（Subsystem=2），不能仅凭构建参数或 NSIS 安装器自己不弹窗判断。

**Windows 后台子进程不弹黑窗**：`-H windowsgui` 只管主 EXE，后台 `exec.Command` 仍须在启动前调用引擎 `executil.HideConsoleWindow`（`HideWindow` + `CREATE_NO_WINDOW`，保留已有进程属性；非 Windows 空操作）。启动时 Python/Node/Git 版本探测、Office 转换，以及引擎 Bash/MCP stdio/hooks 共用它，输出、错误和取消仍走原通道。不要拿它隐藏浏览器、安装器、UAC 或明确请求的 sudo 新窗口；更新看门沿用自己的脱离进程策略。共用包已随引擎 `v0.15.0` 发布。

- `*.exe`（`XRUN.exe`、根目录的 `runcode-desktop.exe` 等）是 `.gitignore` 的构建产物，不进版本库。
- **按品牌打包用 `scripts/build-desktop.sh`**（在 `cmd/runcode-desktop` 下执行），它一次配齐品牌的六处开关——前端 `VITE_BRAND`、Go 窗口标题与单实例锁 `-ldflags`、应用名/产物名、`build/` 下的图标与 macOS `Info.plist`、以及**版本号与产品标识**（`-X internal/desktop.appVersion/.appProduct`，版本更新要用）——构建完自动还原这些打包资产，工作区不留脏改动。手敲 `wails3 task build` 只会改到应用名，成品会出现"界面是智开、bundle 标识符还是 XRUN"这类只在装机后才看得出的错配。
  ```bash
  ./scripts/build-desktop.sh --brand zhikai              # 当前平台
  ./scripts/build-desktop.sh --brand zhikai --universal --zip   # macOS 通用二进制 + 可分发 zip
  ./scripts/build-desktop.sh --test                      # 测试版（含上下文审核）
  ```
  **版本号的唯一事实来源是 `build/config.yml` 的 `info.version`**：它已经在喂 Windows 版本资源、NSIS 安装包与 macOS `Info.plist`，打包脚本再把它注进二进制供"检查更新"比对。发版时改那一处即可；在 Go 里另写一个数的下场是"关于里写 0.2.0、添加删除程序里写 0.1.0"这种装完机才看得见的错配。未经脚本的开发构建版本是 `0.0.0-dev`（比任何正式版都旧，所以更新链路在开发机上也能整条走通）。
- **测试版构建（`--test`）**：注入 `internal/desktop.testBuild` 标记（`internal/desktop/testbuild.go`），解锁仅测试版的"上下文审核"——设置页多一个开关，开启后引擎每次发给模型的完整请求上下文（系统提示词、消息历史、工具清单）按会话落 JSONL 到 `<UserConfigDir>/runcode/context-audit/`，并起一个仅监听 127.0.0.1 的查看页（`internal/desktop/contextaudit*.go`，数据链路是引擎 `Options.LLMRequestObserver`）。前端不用 VITE 开关：设置区块按后端 `ContextAuditStatus().supported` 决定渲染，单一事实来源。正式分发包一律不带 `--test`，此时功能整体不存在（命令拒绝开启、观测器不接线）。

##### macOS 打包

必须在 Mac 上构建（同上，不能交叉编译）。`build/darwin/Info.plist` 是默认品牌的包清单；`build/brands/<品牌>/Info.plist` 覆盖它——**每个品牌的 `CFBundleIdentifier` 必须不同**（XRUN 是 `cn.ouconline.ai.xrun`，智开是 `cn.ouconline.ai.zhikai`），否则 macOS 把两个品牌当成同一个应用，偏好设置、通知授权与 Gatekeeper 记录会互相覆盖。品牌若没有 `build/brands/<品牌>/appicon.png` 就沿用 `build/appicon.png`，三平台同一张图标（智开当前如此）。

分发给他人需签名+公证，否则 Gatekeeper 拦截（自用可右键「打开」绕过）：设 `APPLE_SIGN_ID`（签名）与 `APPLE_KEYCHAIN_PROFILE`（公证）后脚本自动执行。`.app` 压缩必须用 `ditto -c -k --keepParent`，`zip` 不保留符号链接与权限位、会破坏签名。

**macOS 图标与安装包**：四份默认/开发/品牌 plist 的 `CFBundleIconFile` 必须指向实际复制进 Resources 的 `icons.icns`；曾写成 `iconfile`，造成成品没有系统图标。`scripts/verify-macos-bundle.py` 校验完成的 .app 的图标结构、品牌、版本与可执行文件，并通过 `--build-info` 精确核验二进制实际使用的身份（两份 main 均在看门/askpass 之后、任何窗口或用户配置初始化之前返回 JSON）。**不能靠 `go version -m` 检查 ldflags**：Go 的 `-trimpath` 会主动省略该项，缺元数据不等于缺注入；`build/darwin/Taskfile.yml` 必须传递 `LDFLAGS_EXTRA`，否则外层清单虽对、二进制仍是默认品牌/开发版本。品牌脚本在 Mac 上 `--installer --zip` 同时产出 DMG（.app + Applications 链接）和 ditto app.zip；临时 staging 的清理不能覆盖品牌资产还原 trap。CI 挂载 DMG 后再验包并用 iconutil 解码图标，没有 Apple 凭据时仅临时签名、未公证，不保证 Gatekeeper 放行。

**macOS 自动更新（`update_darwin.go`）**：从 `/Applications` 或 `~/Applications` 内的 `.app` 启动才开放「安装并重启」；开发构建、挂载盘、AppTranslocation 仍走手动安装。更新包必须是 `ditto --keepParent` 打的单一顶层 `.app` zip。先做 sha256 快照校验、归档越界/链接检查，再用系统工具校验 bundle ID、版本、架构、完整签名与 Gatekeeper；现有应用有 TeamIdentifier 时新版必须同团队。**发布包必须能通过 `spctl --assess --type execute`（正常分发需签名+公证）**，未签名包只提供手动安装退路，不移除隔离属性、不关闭 Gatekeeper。能写安装目录与旧 bundle 时不提权；需要时走 `sudo -A -k` + 应用密码框，root 私有暂存中再验 zip 哈希与签名。在同卷完整暂存后换名替换，失败恢复旧版；回滚也失败则保留备份路径。正常用户身份的 `open` 等旧 PID 退出后重新打开，新进程不会带 root 权限。`update_bundle_transaction_test.go` 用真实 shell/文件操作验证替换、回滚、信号中断，签名与 libproc 的真机验收仍要在 Mac 上跑。

**通行证令牌的落盘（三平台已各自实现，但依赖系统钥匙串）。** Windows 走 DPAPI（`secret_windows.go`，纯加密、无依赖）；macOS 走钥匙串、Linux 走 Secret Service（`secret_darwin.go` / `secret_linux.go`，共用 `secret_keyring.go` 那层——钥匙串里只放一把主密钥，凭据本身 AES-GCM 加密后留在 `desktop.json`）。**取不到钥匙串就拒绝落盘**，宁可让用户重新登录，也不把密钥和密文一起明文放在同一台机器上。

Linux 上要两个包才能用，少一个都不行（2026-09-18 在麒麟 V10 SP1 真机上查出来的）：`libsecret-tools` 提供 `secret-tool` 命令；`libpam-gnome-keyring` 提供 PAM 模块——麒麟的 `gnome-keyring` 包**只带守护进程、不带 PAM 模块**，而模块正是「图形登录时用登录密码创建并解锁登录钥匙串」的那一步。两个都在 nfpm 的 `recommends` 里，但 **`recommends` 只有 apt 会装，`dpkg -i` 不会**，所以装 deb 请用 `sudo apt install ./zhikai_*.deb`；装完还要注销重登一次图形界面。

这条链路此前有四层静默（PAM 的 `-` 前缀跳过 → 钥匙串没建 → 守护进程弹窗等人 → 我们超时后不落盘），从现象完全回溯不到根因。现在 `secretstatus.go` 把结论变成一句人话加一条可照抄的命令，显示在设置页的「账号(通行证)」里，`persistTokens` 也会记一条日志。

#### 品牌（白标，`frontend/src/core/brand.ts`）

界面上的名字/标记/文案（标题栏、起始页、登录门、空对话引导）全部读 `BRAND`，不在组件里写死。多套品牌都留在 `brand.ts` 的 `BRANDS` 里，构建时选一套——**原品牌永远保留，不是被替换**。基础两套：`runcode`（XRUN，内置 X 矢量标，"AI 编程助手"）、`zhikai`（智开，`@/assets/zhikai-logo.png` 位图，"AI 办公助手"）。换品牌两种等效方式:构建前设 `VITE_BRAND=zhikai`，或改 `brand.ts` 的 `DEFAULT_BRAND` 一行;拼错/未知值一律回落默认品牌（`selectBrand` 有单测）。位图 < 4KB 被 Vite 内联成 data URI，产物自包含、运行期不联网。加品牌只改 `BRANDS`；新增图片走 `@/assets` 导入。

OS 窗口标题（无边框窗口下只在任务栏/alt-tab 显示，UI 标题是前端自绘）在 Go 侧 `main.go` 的 `brandTitle`，默认 `XRUN`，打包智开版时 `wails build -ldflags "-X main.brandTitle=智开"` 配合 `VITE_BRAND=zhikai`。`wails.json` 的 `outputfilename`（exe 名）是静态项，要改 exe 名单独改它。

**国开版智开**：`--brand zhikai-guokai` / `VITE_BRAND=zhikai-guokai`，显示名/EXE 为 `智开（国开版）`，bundle/单实例 ID 为 `cn.ouconline.ai.zhikai.guokai`，更新产品键为 `zhikai-guokai`，不复用普通智开的更新通道。`Brand.scenarioProfile` 选择 `core/scenario-profiles.ts` 的目录：国开版只显示 OA信息查询、格式校验、幻灯片，OA 七项用专属文案；后两类直接复用公共目录，原 XRUN/智开不变。场景选择先于 `visibleScenarios` 的内置动作过滤，不修改生成的 `SCENARIOS`。这只是入口与文案配置，不是工具授权或 OA 权限边界，也不代表账号/数据目录隔离。OA 查询仍需原有后端和用户权限。

#### 前端目录（`cmd/runcode-desktop/frontend/src`）

跨目录一律用 `@/` 别名（`vite.config.ts` 的 `resolve.alias` + `tsconfig.json` 的 `paths`），同目录内保持 `./` 相对路径——这样挪文件不牵动导入方。分层自下而上，依赖只朝下：

| 目录 | 职责 |
| --- | --- |
| `core/` | 后端通道与领域纯逻辑：`bridge`、生成的 `protocol/`、`paths`、`format`、`tool-catalog`（内置工具中文目录）、`custom-models`、`passport-account`、`brand`（白标品牌：名字/标记/文案，构建时选一套） |
| `ui/` | 与业务无关的通用件：`tokens`（BTN/DRAG 等类名常量）、`feedback`（**错误/警告的唯一出口**，见下）、`layout`（`PageShell` 管理页骨架 / `Placeholder` 空态与加载态 / `InsetRow`+`INSET_BOX` 设置行）、`keys`（`isComposingKey`：输入法组字判定，凡是把 Enter 当快捷键的地方都要先过它）、`icons`、`markdown`、`popover`、`fields`、`model-picker`、`badges`、`glyphs`、`toggle`、`confirm-dialog`… |
| `hooks/` | 跨模块通用钩子：`use-stick-to-bottom`、`use-persistent-state` |
| `chat/` | 对话渲染层：`blocks`（分组/合并纯逻辑）、`tool-text`（工具事件→中文，纯函数）+ 各类卡片组件 |
| `preview/` | 预览：`classify`/`tabs`（纯逻辑）、`file-panel`/`diff-panel`/`pane`/`file-browser`，Office 查看器在 `viewers/` |
| `composer/` | 输入区：`keymap`（按键归属：输入法组字 > 候选框 > 发送/换行，纯函数）、`mention`（触发解析与候选排序，纯函数）、`paste`（粘贴/拖放的附件：收哪些、怎么命名、非图片文件怎么写进正文，纯函数；拖放另需 `main.go` 的 `EnableFileDrop` 与输入区的 `data-file-drop-target`）、`mention-picker`、`toolbar`、`index` |
| `pages/` | 整页：`plugins/`、`permissions`、`mcp`、`memory`、`start/`、`settings/` |
| `session/` | 应用状态与副作用钩子：`use-conversation`（引擎事件订阅在此）、`use-session`、`use-plan`（阶段化计划模式的运行状态与审批草稿）、`use-permission-queue`、`use-preview-panel`、`use-workspace-files`、`use-auto-preview`、`use-toast`、`use-update`（版本更新；状态在 Go 侧，这里只做它的镜子）、`use-scenario-actions`（场景关联技能安装与填入草稿）、`use-recording-minutes`（录音结束去重与速览/纪要流程，复用 `recorder/minutes` 纯函数） |
| `shell/` | 外壳组件：`title-bar`、`status-bar`、`chat-pane`、`preview-side`、`permission-modal`、`sidebar` |
| `dev/` | 预览页，不进正常流程：`?preview=ui` 公共组件画廊（**改样式后的视觉回归就看它**——自动化检查证明不了观感）、`?preview=tools` 工具卡、`?preview=thinking` 思考面板 |

`App.tsx` 只负责把上述钩子接起来并按视图摆放 shell 组件，不放具体逻辑。场景/纪要的异步结果受 `session/action-scope` 会话代际约束，切走再切回也不复活旧动作。`core/ui/hooks` 的反向导入由 ESLint 拦截，跨目录使用 `@/`，不得用相对父目录或动态导入绕过。改行为找 `session/`，改样子找 `shell/` 或对应页面。

**公共组件与设计 token 的完整说明在 [`frontend/src/ui/README.md`](cmd/runcode-desktop/frontend/src/ui/README.md)**——token 对照表、每个组件的用法与**何时不该用**、以及每条约束各自是从哪次真实事故来的。写前端前先看它，改 `ui/` 下的东西请同步它。铁律五条：

1. **颜色 / 圆角 / 阴影 / 字号只用 token**，不写 `#hex`、`rgba()`、`rounded-[8px]`、`text-[12.5px]` 这类字面值（三类例外见文档）。透明度用斜杠语法 `border-red/35`。
2. **字号只有整数 px，没有半档**（`9 10 11 12 13 14 15 16 18 20 22 24 26`，主力 12/13）。要拉层次跨整档——0.5px 在本应用的 DPI 下看不出来，只会变成下一个人的困惑。
3. **报错与警告只从 `ui/feedback` 出**：对话流用 `SystemNote`（居中分割线条，**不是气泡**——只有模型说的话才进助手列 `BotRow`），弹窗表单用 `Banner`，页面级失败用 `InlineError`。严重度只有 `Tone` 一个维度，它同时决定颜色与字号，调用点别另挑字号。警告文字用 `text-amberink` 不是 `text-amber`（后者对比度约 2.3:1，小字不达标）。
4. **管理页用 `PageShell`**（mcp / memory / settings）。plugins 与 permissions 有各自的固定页头与更宽版心，不套这层。
5. **把 Enter / 方向键当快捷键前先过 `isComposingKey`**，否则中日韩输入法组字时会被抢键。

前端自检（在 `frontend/` 下）：`npm run typecheck`、`npm run lint`、`npx vitest run`、`npm run build`。**`npm run lint` 不是可选项**——`tsc` 证明不了 `useEffect` 少列依赖，`react-hooks/exhaustive-deps` 才管这件事，`session/` 下的钩子全靠它兜底。要豁免必须写 `// eslint-disable-next-line` 并在上方注明为什么（现有两处：通行证协调器只建一次、起始页自动进入只评估一次）。纯逻辑模块（`chat/tool-text`、`chat/blocks`、`chat/plan-draft`（审批区清单的增删/排序/整理）、`preview/classify`、`preview/tabs`、`composer/mention`、`composer/keymap`、`composer/paste`、`ui/keys`、`ui/model-picker`（`toModelOptions`：平台+自定义候选合并，输入框与设置页共用）、`pages/mcp-draft`、`core/custom-models`、`core/passport-account`、`core/brand`）都有单测，新增纯函数请一并补测。

### 本地交互网页与环境提示

- 会话环境来自引擎动态提示词：当前工作区、系统/进程架构、每次主请求刷新的日期和实际 Shell。工作区不是技能目录；技能相对资源按 Skill 返回的目录定位，产物默认留在工作区，不从当前界面聚焦目录猜另一会话的路径。
- 桌面专属 `internal/browsertool` 的 `open_browser` 只接受本机回环 HTTP(S) URL，经 `ExtraTools` 注册、`ClassMutating` 分类，沿用审批/计划模式/OA 闸门，不注入子代理。先用有界 TCP 探测端口，不发业务 HTTP 请求；不硬编码端口。它是系统浏览器打开入口，不是浏览器自动化或网络沙箱；网页后续导航/请求仍按浏览器正常规则执行。返回成功只代表已发起打开，不代表页面渲染或用户确认。
- `desktop/browser*.go` 与通行证/Codex 登录复用平台打开器，模型 URL 的回环限制在工具层（登录仍可开远程认证页）；不经 shell 拼接 URL。`browser.go` 的桌面提示说明 `open_preview` 与网页的区别，以及 Bash 前台最多 120 秒、长时间等待用户确认应走后台命令和 BashOutput。不能伪造确认结果或反复启动服务器。
- 原生浏览器冒烟测试为显式 opt-in：`RUNCODE_BROWSER_SMOKE=1 go test ./internal/desktop -run '^TestBrowserNativeSmoke$' -count=1 -v`，会真实打开一个无账号/项目数据的本地测试页；普通测试不会打开浏览器。历史 ppt-master 记录不替代当前技能版本的真机验收。环境段改动已随引擎 `v0.15.0` 发布。

### 提问重生成

- 用户气泡的“重新生成 / 修改后重试”创建独立会话分支，不是追加同一句话，也不改写源历史、不回滚文件。入口在 `desktop/questions.go` 与 `session/use-question-retry.ts`，编辑前的文字/附件草稿按会话保存，取消时恢复。
- 提问按来源 `sessionId + questionId` 定位。新输入经 `host.SubmitQuestion` 确认落盘后才返回回执（中途补充在真正纳入历史时确认，取消不会挂住调用方）；旧记录由 `sessions.IdentifyQuestions` 生成稳定兼容 ID，不重写原文件。渲染块 ID 与提问 ID 不能混用，气泡缩略文案不能作为原始输入。
- 分支读取 **完整持久化历史**，不能拿已压缩的 `Session.History()` 或前端数组截断。前缀保留 thinking、图片、成对工具记录；原图用来源绑定的索引复用持久化字节，不能凭文件名猜路径。回放也必须保留 thinking 与纯图片提问。
- 分支继承来源条目的连接及当前运行时设置，不能从当前聚焦会话猜配置。OA 锁在复制历史前落盘，空前缀也要恢复计划模式等元数据；一次性授权不复制。创建不抢焦点，前端先回放再聚焦/提交，异步期间切换或关闭使旧动作失效。初次提交失败保留修改稿，同一分支不能重复作为未执行的重试入口。
- `host.WithIdleSession` 将分支读取/手动压缩与回合、设置变更、关闭隔离；`OnTurnStart` 在准入后、任何模型/工具执行前初始化编辑与计划记账，不在异步提交回执之后清基线。本功能已随引擎 `v0.15.0` 发布。

### OA 附件下载

`internal/oatool` 的 `oa_attachments` 列出文档/流程附件，`oa_download_attachment` 经 Bridge 的 `/[t/{tenantId}/]v1/oa/attachments/download` 下载到**调用会话**工作区的 `OA附件/download-<随机值>/`。后者是 `ClassMutating`（远端只读，但会写本地），其余 OA 查询仍是只读；沿用本地模型 Gate、OA 会话锁及子代理不注入 OA 工具的规则。参数只接受来源类型、来源 ID、附件 ID，不允许任意 URL、userid 或目标路径。Go rooted filesystem 防止链接越界，独立目录+排他创建防覆盖，限制 100 MiB，断流/取消清理半成品；不会自动解压、执行或预览。

需要同时部署 Bridge 与 Python OA 服务的新接口，仅更新桌面不生效。Python 每次下载重新读取来源并核对附件归属，用该用户的 OA Session 下载；不把 JSESSIONID 发给桌面。当前解析同源 FileDownload 附件链接与明确的 fileid/filename 记录（包括嵌套表单），扫描图保留原行为；未知 OA 表单结构明确提示暂未识别，不能当作“没有附件”。真实部署字段仍需脱敏样本验收，合成 fixture 不替代真机验证。

**附件标识不能直接当物理文件 ID（2026-09-24 错文件回归）**：Python 的 `oa_attachment_refs.py` 保留表单 `fileid` 作为逻辑附件引用，实际下载取同记录的 `imagefileid` 或经当前用户鉴权的 `/api/doc/save/getAccListForEdit?id=<文档ID>` 返回值（该接口的 `dataList[].id` 是文档 ID、`fileid` 才是物理文件 ID）。不能猜同号文件，多条/不一致/未知映射一律拒绝。`oa_attachment_names.py` 解码旧式百分号文件名、对比源/响应文件名并阻止明显的 docx→OLE 错配（加密 OOXML 也会拒绝）。新客户端要求每次实际下载响应带 `X-OA-Attachment-Contract: 2`；Bridge 0.2.2 校验并转发此标记、把已知错误码转成固定中文说明；OA 服务对应 0.5.2。只更新 EXE、旧 Bridge 丢失标记、滚动升级打到旧 OA Pod，都会拒绝保存而非降级到旧下载逻辑。需先部署配套服务再验收；不把合成回归当作真实 OA 字段验收。

### 模型图片能力与默认识图

- **平台默认识图**：Bridge 模型目录用独立的 `vision-default: true`（唯一且须 `supports-images: true`），只向获授权租户下发 `vision_default`。桌面未手选时跟随调用会话的平台/租户，手选优先，`Vision.Disabled` 明确关闭；不把平台默认落盘成手选。自定义会话创建/切换/分支也保留绑定租户，不从焦点猜。目录按 Bridge/租户/账号分区，60 秒 TTL、有界刷新，迟到响应不能串账号；回合冻结目标，缺默认/无授权/过期刷新失败只在需要识图时明确报错，不阻断纯文字。删除手选自定义识图模型会关闭兜底，防止悄悄改投平台；OA 第二识图连接禁令不变。需要新版 Bridge 与桌面，既有 0.2.3 镜像不含此新增功能。

- 图片能力是**每个模型的三态** `SupportsImages *bool`：true 原图直传，false 使用默认识图，nil 未标注兼容原图直传。不能按名称/provider 猜。自定义模型编辑省略字段保留原标记，`ClearImageSupport` 才清除；平台本机覆盖按 Bridge + 租户 + 模型定位，上游可提供 `supports_images`。设置页“图片识别”只允许明确支持图片的候选。
- 视觉配置只经 `GetVisionSettings` / `SaveVisionSettings` / `SetPlatformImageCapability`，不塞入启动/普通设置的旧快照。默认保存结构化 `ModelReference`，不复制密钥；自定义重命名同步引用，删除默认模型清除选择。活会话绑定自己的引用，回合开始冻结配置，不从焦点猜。平台凭据可正常刷新，登录账号变化使旧请求失效；Codex 识图使用临时的固定上游代理，不复用会随配置编辑改目的地的聊天代理。
- 引擎 `Options.Images` / `imageinput` 统一处理用户图、Read/MCP 嵌套工具图和子代理。仅文本模型的请求用文字投影，原图、提问和主模型不变；新图先识别再算预算，旧图用会话内容指纹引用按需查看。`analyze_image` 只接受已授权引用与问题，不接受任意路径/URL/目的模型；文件先走 Read 原授权。未注入端口的 CLI/server 不变。
- 宿主 `internal/vision` 只发送图片和当前必要问题，无主历史/工具；120 秒、全进程最多两个推理请求、至多两次分析尝试（空白/仅思考/截断或可重试传输失败；provider 自动传输重试关闭，以便每次发送前重查 OA/账号 gate；平台 401 仍可刷新一次）、每图 8 MiB、每次至多 8 张、本轮至多 32 张新图、输出有上限。仅 URL、格式不符、空/仅思考/截断/取消或未配置明确报错，不悄悄直传给仅文本模型。思考保留在分析记录，参与压缩；识图 API 用量单列，分析文字仍计入主上下文。
- **OA 锁定会话禁止任何第二识图连接**（包括自定义/Codex），缓存读取与每次推理都再过调用会话的 live gate；需要支持图片的原绑定内网模型，不用“已选默认模型”绕开 OA 边界。
- `.runcode/image-analysis/<会话ID的sha256>.json` 是有界、原子、rooted 的派生记录，不含原图/凭据；恢复展示真实已保存结果。分支只复制问题和全部图片均属于前缀的分析，重新绑定图片引用，不能把后续分析倒灌到过去；损坏按缺缓存处理。删除会话前须关闭它，不能删除仍在后台持有历史/分析写入器的非聚焦会话。
- 所需 agentloop API 已随 `v0.15.0` 发布，三个 module 固定到该 tag；真实第三方模型兼容性、真实 OA 图片与其它平台 GUI 不能拿合成 HTTP/UI fixture 冒充验收。
