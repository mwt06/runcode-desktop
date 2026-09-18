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

`go.work` 已提交（use：`.`、`./cmd/runcode-desktop`、`./cmd/runcode-server`、`../agentloop`）供本地联动；**CI/发布链路一律 `GOWORK=off`**，此时引擎按 go.mod require 的 tag 版本经 `GOPRIVATE` 从内网 GitLab 解析——引擎改动须打新 tag 并升 require 才进 CI/发布（GitHub CI 够不着内网 GitLab，见 `.github/workflows/ci.yml` 顶部 TODO）。

### 常用命令（根目录执行）

- 全量编译：`go build ./... ; go -C cmd/runcode-server build ./... ; go -C cmd/runcode-desktop build ./...`（或 `make build`）。
- 测试（CI 用 `-race`，三平台）：`go test -race ./... ; go -C cmd/runcode-server test -race ./...`（或 `make test`）。
- Lint：`golangci-lint run`（或 `make lint`；配置根 `.golangci.yml`，启用 gosec/errcheck/gocritic）。
- 协议 TS 再生成（引擎 protocol 变更后）：`go run ./tools/protogen`；CI 用 `--check` 防漂移。
- 打运行时包（Python/Node/Git 的绿色包，传 OBS 用）：`go run ./tools/runtimepacks --list` 先看要下什么，再去掉 `--list` 实打；见 `tools/runtimepacks/main.go` 的说明。
  运行时清单有两条路：默认走 Bridge 的 `/api/app/runtimes`；打包时加 `--runtime-manifest 'https://<obs>/runtimes-{platform}.json'` 则改走对象存储上的静态文件（`{platform}` 由客户端换成 `windows-amd64` 这种，见 `runtimeManifestURL`），**不需要服务端**，代价是换版本要重传 JSON。
- 出 CLI 二进制：`go build -o runcode.exe ./cmd/runcode`。

### `internal/` 文件分工

两个大包，各自一个包内按职责分文件（Go 里"目录结构"就是包，包内靠文件名分工），外加一个只放类型的 `internal/protocol`：

- **`internal/protocol`**（桌面自己的 wire 类型：设置表单、通行证、技能/子代理/MCP/工具管理页、编辑复审、harm 提示）。**加字段、加 DTO 请加在这里，不要加进引擎**——引擎的 `agentloop/protocol` 只负责"跑一个回合"的契约（assistant delta / 工具事件 / 审批 / 回合结果 / 会话状态 / 错误 / envelope），且与 `cmd/runcode-server` 共享。判据是"谁产生它"：引擎 `host` 包产生或消费的归引擎，只有本外壳用的归这里，**没有例外**。命令清单 `CommandKinds` 就在这里（`internal/protocol/commands.go`）——**新增一个 Wails 命令只改本仓**，引擎不必发版；引擎只保留分类词汇 `protocol.CommandKind` 与三个常量（"query 意味着什么"两端必须一致，"有哪些命令"各自声明，`cmd/runcode-server` 另有自己的一份）。两个包由 `tools/protogen` 合并生成同一份 TS，重名会直接报错。
- **`internal/desktop`**（桌面核心，Wails 把 `App` 的导出方法绑给前端，所以命令必须都挂在同一个类型上，不能拆包）：`app.go` 只留 App 结构与会话开关；回合在 `turn.go`、自动标题在 `title.go`、运行中可变的会话设置在 `session_settings.go`；其余按功能各自成文件（`skills` / `agents` / `mcp` / `passport` / `oauth` / `tokens` / `preview` / `editstore` / `plan`（阶段化计划模式的阶段机与审批闸门）/ `custommodels` / `config` / `store` / `disabled` / `harm` / `appdirs`（本应用自己的安装/数据目录不再走"项目外授权"，包一层 `permissions.Policy`）/ `update`（版本更新的状态机：查网关清单 → 下载 → 校验 sha256 → 拉起安装器；`version.go` 是版本号与产品标识，两者都由打包脚本经 `-ldflags` 注入）/ `download`（带进度与 sha256 的大文件下载，更新、技能市场与运行时包三处共用） / `runtimes`（运行时环境：Python/Node/Git 的绿色包，查清单 → 下载 → 校验 → 解压到用户目录，**全程不提权**；`runtimepack.go` 是"本机知识"——包内布局与系统探测，故意不来自服务端清单；`runtimeenv.go` 把它接进 PATH 与 `SystemPromptAppend`，两者缺一不可：只给 PATH 模型不知道 Python 在那儿，还会回一句"请先安装 Python"。**PATH 只写进本进程、不放进 `engine.Config.ToolEnv`**：ToolEnv 是开对话那一刻的快照，放进去就把这个对话的 PATH 冻住了，对话中途装上的运行时在这个对话里永远找不到；引擎的 Bash 每条命令都现读 `os.Environ()`，所以写进程 PATH 就够。模型那条安装路在 `runtimetool.go`（清单没取就先取、设置页正在装就等、装好没放行就补授权；`runtimeResolver` 给审批弹窗配"装什么、多大"那句话——引擎的审批请求里不带工具参数）；`untargz.go` 是它专用的解压器——运行时包用 tar.gz 而非技能包那种 zip，因为里面有符号链接与可执行位） / `privilege` + `askpass`（让模型用 sudo，**两道人工关**：先在应用里审批那条命令——永不记住、永不被裁判自动放行——再在应用自己的弹框里输系统密码，模型永远看不到。askpass 就是本应用二进制自身（`IsAskpass`/`RunAskpass`，`main.go` 与 `main_kylin.go` 都要在单实例锁之前处理掉）；服务端对每个连接核验来者：同一用户、是本应用二进制、**父进程名为 sudo 且有效 UID 为 0**——只看名字会被一个叫 sudo 的脚本骗过（麒麟真机实测）。仅 Linux 开放，且要求 `DISPLAY` 非空（sudo 在无终端时自动改走 askpass 的前提）；别的平台 sudo 保持引擎原来的硬拒。sudo 下的 rm/dd/mkfs 等仍然硬拒，pkexec/doas 一律拒——引擎的特权名单漏了它们，pkexec 会绕开应用内审批。端到端测试 `askpass_e2e_test.go` 带构建标记，要在真 Linux 上用真 sudo 跑）/ …）。技能与子代理共用的作用域目录解析与命名规则在 `resources.go`（`resourceRoot(kindSkills|kindAgents, scope)`），别再各写一份。
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

**KYSEC 执行控制（麒麟 V10 SP1 真机查实，影响一切"在麒麟上执行我们自己放下去的文件"的功能）**：经 dpkg 装的文件自动标为 `verified`（`sudo kysec_get <文件>` 可查），我们自己写出/解压出的 ELF 是 `unknown`，**执行会被拒**（`权限不够`，退出码 126；所谓 warning 模式实际是桌面弹一个 30 秒的确认框，没人点就拒）。它**连 `.so` 的加载也管**：可信的解释器去加载一个 unknown 的扩展模块同样失败（`failed to map segment from shared object`）。`sudo kysec_set -n exectl -v verified <文件>` 可以加白。推论：askpass 必须指向 deb 装的应用本体而不是临时脚本。弹框的实际规则（逐条实测）：**只在启动进程时弹**，允许之后该进程加载的 unknown `.so` 一路放行；**允许不会被记住**，下次启动照弹；而 verified 的进程去加载 unknown `.so` 会被拒——所以只给解释器加白不够。运行时包因此在装完后用一次 `sudo -A kysec_set` 把包里**全部 ELF**（按文件头认，不按扩展名）加白，走应用的密码框、带用途说明（`kysec.go` / `kysec_linux.go`，`getstatus` 不用 root 就能判断执行控制开没开）；加白时记下这批 ELF 的指纹，之后 pip 装了带 C 扩展的新库、指纹一变就重新亮出「授权」按钮。

**一个尚未在真机验证的前提**：V10 基础版的 WebKitGTK 是 2.28.1，而 `10.1-2403-updates` 源里是 2.38.6。2.38.6 支持 `@layer` 与 `color-mix()`，现有的 Tailwind v4 前端只损失 `@property`（被 `@supports` 包着，表现是渐变等效果打折）；停在 2.28.1 的机器则需要额外的样式降级。判定方法是在目标机器上跑 `apt policy libwebkit2gtk-4.0-37`。CI 只验证了能编译能出包，**没有任何一台真实麒麟跑过**。
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

**通行证令牌的落盘（三平台已各自实现，但依赖系统钥匙串）。** Windows 走 DPAPI（`secret_windows.go`，纯加密、无依赖）；macOS 走钥匙串、Linux 走 Secret Service（`secret_darwin.go` / `secret_linux.go`，共用 `secret_keyring.go` 那层——钥匙串里只放一把主密钥，凭据本身 AES-GCM 加密后留在 `desktop.json`）。**取不到钥匙串就拒绝落盘**，宁可让用户重新登录，也不把密钥和密文一起明文放在同一台机器上。

Linux 上要两个包才能用，少一个都不行（2026-09-18 在麒麟 V10 SP1 真机上查出来的）：`libsecret-tools` 提供 `secret-tool` 命令；`libpam-gnome-keyring` 提供 PAM 模块——麒麟的 `gnome-keyring` 包**只带守护进程、不带 PAM 模块**，而模块正是「图形登录时用登录密码创建并解锁登录钥匙串」的那一步。两个都在 nfpm 的 `recommends` 里，但 **`recommends` 只有 apt 会装，`dpkg -i` 不会**，所以装 deb 请用 `sudo apt install ./zhikai_*.deb`；装完还要注销重登一次图形界面。

这条链路此前有四层静默（PAM 的 `-` 前缀跳过 → 钥匙串没建 → 守护进程弹窗等人 → 我们超时后不落盘），从现象完全回溯不到根因。现在 `secretstatus.go` 把结论变成一句人话加一条可照抄的命令，显示在设置页的「账号(通行证)」里，`persistTokens` 也会记一条日志。

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
| `composer/` | 输入区：`keymap`（按键归属：输入法组字 > 候选框 > 发送/换行，纯函数）、`mention`（触发解析与候选排序，纯函数）、`paste`（粘贴/拖放的附件：收哪些、怎么命名、非图片文件怎么写进正文，纯函数；拖放另需 `main.go` 的 `EnableFileDrop` 与输入区的 `data-file-drop-target`）、`mention-picker`、`toolbar`、`index` |
| `pages/` | 整页：`plugins/`、`permissions`、`mcp`、`memory`、`start/`、`settings/` |
| `session/` | 应用状态与副作用钩子：`use-conversation`（引擎事件订阅在此）、`use-session`、`use-plan`（阶段化计划模式的运行状态与审批草稿）、`use-permission-queue`、`use-preview-panel`、`use-workspace-files`、`use-auto-preview`、`use-toast`、`use-update`（版本更新；状态在 Go 侧，这里只做它的镜子） |
| `shell/` | 外壳组件：`title-bar`、`status-bar`、`chat-pane`、`preview-side`、`permission-modal`、`sidebar` |
| `dev/` | 预览页，不进正常流程：`?preview=ui` 公共组件画廊（**改样式后的视觉回归就看它**——自动化检查证明不了观感）、`?preview=tools` 工具卡、`?preview=thinking` 思考面板 |

`App.tsx` 只负责把上述钩子接起来并按视图摆放 shell 组件，不放具体逻辑。改行为找 `session/`，改样子找 `shell/` 或对应页面。

**公共组件与设计 token 的完整说明在 [`frontend/src/ui/README.md`](cmd/runcode-desktop/frontend/src/ui/README.md)**——token 对照表、每个组件的用法与**何时不该用**、以及每条约束各自是从哪次真实事故来的。写前端前先看它，改 `ui/` 下的东西请同步它。铁律五条：

1. **颜色 / 圆角 / 阴影 / 字号只用 token**，不写 `#hex`、`rgba()`、`rounded-[8px]`、`text-[12.5px]` 这类字面值（三类例外见文档）。透明度用斜杠语法 `border-red/35`。
2. **字号只有整数 px，没有半档**（`9 10 11 12 13 14 15 16 18 20 22 24 26`，主力 12/13）。要拉层次跨整档——0.5px 在本应用的 DPI 下看不出来，只会变成下一个人的困惑。
3. **报错与警告只从 `ui/feedback` 出**：对话流用 `SystemNote`（居中分割线条，**不是气泡**——只有模型说的话才进助手列 `BotRow`），弹窗表单用 `Banner`，页面级失败用 `InlineError`。严重度只有 `Tone` 一个维度，它同时决定颜色与字号，调用点别另挑字号。警告文字用 `text-amberink` 不是 `text-amber`（后者对比度约 2.3:1，小字不达标）。
4. **管理页用 `PageShell`**（mcp / memory / settings）。plugins 与 permissions 有各自的固定页头与更宽版心，不套这层。
5. **把 Enter / 方向键当快捷键前先过 `isComposingKey`**，否则中日韩输入法组字时会被抢键。

前端自检（在 `frontend/` 下）：`npm run typecheck`、`npm run lint`、`npx vitest run`、`npm run build`。**`npm run lint` 不是可选项**——`tsc` 证明不了 `useEffect` 少列依赖，`react-hooks/exhaustive-deps` 才管这件事，`session/` 下的钩子全靠它兜底。要豁免必须写 `// eslint-disable-next-line` 并在上方注明为什么（现有两处：通行证协调器只建一次、起始页自动进入只评估一次）。纯逻辑模块（`chat/tool-text`、`chat/blocks`、`chat/plan-draft`（审批区清单的增删/排序/整理）、`preview/classify`、`preview/tabs`、`composer/mention`、`composer/keymap`、`composer/paste`、`ui/keys`、`ui/model-picker`（`toModelOptions`：平台+自定义候选合并，输入框与设置页共用）、`pages/mcp-draft`、`core/custom-models`、`core/passport-account`、`core/brand`）都有单测，新增纯函数请一并补测。
