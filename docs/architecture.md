# runcode 架构

本文档从当前代码整理,描述 runcode 仓库——引擎的三个**外壳**——的分层结构与模块边界。引擎本体(ReAct 循环、工具、权限、provider、持久化)在独立仓库 **agentloop**,内部细节见[彼侧文档](#引擎内部请见-agentloop);桌面版(XRUN)细节见 [docs/desktop.md](./desktop.md)。

## 总览:三个外壳,一个外部引擎

```text
┌ cmd/runcode chat ┐ ┌ cmd/runcode tui ┐ ┌ XRUN 桌面版(Wails)      ┐ ┌ cmd/runcode-server    ┐
│ shell 友好 CLI    │ │ Bubble Tea TUI  │ │ internal/desktop + 前端 │ │ 服务端交接骨架          │
└────────┬─────────┘ └───────┬─────────┘ └───────────┬────────────┘ └──────────┬────────────┘
         └───────────────────┴───────────┬───────────┴───────────────────────────┘
                                         │
                  gitlab.ouc-online.com.cn/aibase/agentloop(外部 module)
                  engine.Build(cfg, Options{...}) → *engine.Session
                  多会话外壳(桌面/服务端)另经 host.Manager
```

各外壳经 `engine.Build` 拿到 `*engine.Session`,注入各自的流式回调、工具事件通道与审批器;引擎内部不含任何 UI 逻辑。多会话外壳(桌面、服务端)不直接拼 Session,而是复用引擎的 `host` 包(会话表、事件信封、审批路由、并发配额)。

## 模块边界(依赖方向)

本仓库含三个 Go module,引擎是第四方——**外部依赖**:

- **根模块**(`github.com/wt68/runcode`):CLI/TUI、桌面核心(`internal/desktop`)、`internal/previewtool`、`tools/protogen`。
- **`cmd/runcode-desktop/`**(嵌套 module):Wails/CGO 重依赖隔离层,`replace` 指回根模块与引擎。
- **`cmd/runcode-server/`**(嵌套 module):独立仓库服务端的可跑参考实现,自带依赖审计测试(`deps_test.go`——只许 import agentloop 公开面与自身)。
- **引擎**(`gitlab.ouc-online.com.cn/aibase/agentloop`):require 固定 tag(无 replace)。开发时经 `go.work` 与同级 checkout `../agentloop` 联动;`GOWORK=off` 构建经 `GOPRIVATE=gitlab.ouc-online.com.cn` 直连内网 GitLab 拉取 tag。

依赖方向:**外壳 → agentloop**,反向不存在("引擎永不依赖外壳"的纪律由 agentloop 仓库自己的 CI 审计)。`go.work`(use:`.`、`./cmd/runcode-desktop`、`./cmd/runcode-server`、`../agentloop`)供本地联动;CI/发布一律 `GOWORK=off`,引擎按 require 的 tag 版本解析——引擎改动须打新 tag 并升 require 才进 CI/发布。

## 仓库布局

```text
cmd/runcode/             Cobra CLI:version、chat、tui、config、permissions、sessions、transcript
cmd/runcode-desktop/     嵌套 Go module:Wails 桌面外壳 + React 前端(见 docs/desktop.md)
cmd/runcode-server/      嵌套 Go module:服务端交接骨架(HTTP/SSE,只依赖引擎公开面)
internal/desktop/        桌面版传输无关核心(根模块内,不依赖 Wails,可单测)
internal/ui/             Bubble Tea TUI:视图、slash 命令注册表、会话选择器、审批桥
internal/command/        自定义 slash 命令(*.md 发现)
internal/previewtool/    桌面产物预览工具(经 ExtraTools 注入,仅桌面用)
internal/skilltool/      桌面版 Skill 工具(经 Options.SkillTool 替换内置;披露不变,额外发技能卡片事件)
tools/protogen/          协议 TS 代码生成器(读 agentloop/protocol,写前端 src/protocol/)
```

## 外壳如何消费引擎

- **CLI(`cmd/runcode` chat)**:`resolveChatConfig` 把 flag/env/TOML 解析为 `engine.Config`,`engine.Build` 后注入 `StreamDelta`(stdout 流式)与非交互/交互审批器;`sessions`/`transcript`/`config`/`permissions` 子命令直接调用引擎的 `sessions`、`transcript`、`settings`、`permissions` 公开包。
- **TUI(`cmd/runcode` tui + `internal/ui`)**:同一条 config 解析链;Bubble Tea 模型消费 `StreamDelta`/`StreamThinking`/`ToolEvents`,`internal/ui/approval.go` 把 `permissions` 审批请求桥接为模态框(工作区内路径脱敏成相对路径;越出工作区的路径按**绝对路径**整条展示,并注明"允许"记住的是哪个目录);slash 命令注册表(`/help`、`/clear`、`/compact`、`/status`、`/mode`、`/model`、`/cost`、`/exit`)合并 `internal/command` 发现的自定义 `*.md` 命令。
- **桌面(`internal/desktop` + `cmd/runcode-desktop`)**:`desktop.App` 是 `host.Manager` 之上的薄适配层(`host.NewManager(host.Options{Build: host.DefaultBuild, ...})`,单用户外壳不设配额);命令/事件全部走 `agentloop/protocol` 的 wire 类型,harm judge、审批路由、会话恢复经 host 层。Wails 外壳只做事件桥、原生对话框与 `Bind(app)`。
- **服务端骨架(`cmd/runcode-server`)**:同样架在 `host` + `protocol` 上的 HTTP/SSE 参考实现,演示"独立仓库服务端"的最小可跑形态;`deps_test.go` 强制它只 import 引擎公开面。

## 协议与代码生成

wire 协议的单一事实源是 agentloop 的 `protocol` 包(stdlib-only)。本仓库的 `tools/protogen` 读取该包与 `internal/desktop` 的 App 命令面,生成前端 `cmd/runcode-desktop/frontend/src/protocol/{types,events,commands}.ts`(判别联合、EventMap、typed 命令包装)。引擎 protocol 变更后运行 `go run ./tools/protogen` 再生成;CI 用 `--check` 防漂移。协议详情见 agentloop 的 `docs/protocol.md`。

## CLI 参考(cmd/runcode)

七个子命令:`version`、`chat`(`--loop` 多轮)、`tui`(`--pick` 会话选择器)、`config`(打印生效配置与来源,凭证脱敏)、`permissions`、`sessions`(`list`/`show`)、`transcript`(`list`/`search`)。

主要 flag 与环境变量(`chat`/`tui` 共用,config 解析在 `resolveChatConfig`):

| flag | env |
|------|-----|
| `--provider` | `RUNCODE_PROVIDER` |
| `--model` / `--max-tokens` / `--base-url` | `ANTHROPIC_MODEL` / `ANTHROPIC_MAX_TOKENS` / `ANTHROPIC_BASE_URL` |
| `--api-key` / `--auth-token` | `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` |
| `--cwd` | `RUNCODE_CWD` |
| `--permission-mode safe\|interactive` | `RUNCODE_PERMISSION_MODE` |
| `--thinking off\|low\|medium\|high` | `RUNCODE_THINKING` |
| `--system-prompt` / `--append-system-prompt` | `RUNCODE_SYSTEM_PROMPT` / `RUNCODE_APPEND_SYSTEM_PROMPT` |
| `--telemetry off\|jsonl` | `RUNCODE_TELEMETRY` |
| `--transcript off\|jsonl\|sqlite` / `--session-id` | `RUNCODE_TRANSCRIPT` / `RUNCODE_SESSION_ID` |
| `--resume` / `--continue` / `--no-session` / `--session-backend` | `RUNCODE_SESSION_BACKEND` 等 |
| `--max-history-messages` / `--max-context-tokens` | `RUNCODE_MAX_HISTORY_MESSAGES` / `ANTHROPIC_MAX_CONTEXT_TOKENS` |
| `--max-retries` / `--input-price` / `--output-price` | `RUNCODE_MAX_RETRIES` / `RUNCODE_INPUT_PRICE` / `RUNCODE_OUTPUT_PRICE` |
| `--allow-mcp-sampling` | `RUNCODE_ALLOW_MCP_SAMPLING` |

注意:CLI/TUI 的权限模式只接受 `safe`/`interactive`;`judge`/`flight` 与 harm judge 配置(`RUNCODE_HARM_JUDGE_MODEL`/`RUNCODE_HARM_JUDGE_VOTES`)目前是桌面版专属。

## 引擎内部请见 agentloop

以下主题的文档随引擎迁入 agentloop 仓库(同级 `../agentloop`),本仓库不再复述:

- agentloop `README.md` —— 引擎消费者指南(如何 Build 会话、换提示词、加工具)
- agentloop `docs/engine-api.md` —— 门面 API(Config/Options/Build/Session)
- agentloop `docs/protocol.md` —— 双端 wire 协议与代码生成约定
- agentloop `docs/server-handoff.md` —— 服务端开发交接

涵盖:ReAct 循环、权限系统(四模式 + harm judge)、工具系统(14 内置工具 + MCP/Skill/Task/Remember)、提示词装配与缓存边界、provider 层(anthropic/openai)、会话/transcript 持久化、host 多会话层、MCP/skills/子代理/记忆/hooks 扩展系统。

## 验证

```bash
go build ./...                          # 根模块全量编译
go test -race ./...                     # CI 同款
go -C cmd/runcode-server build ./...    # 服务端骨架
go -C cmd/runcode-desktop build ./...   # 桌面 Go 侧快速检查
cd cmd/runcode-desktop && wails build   # 桌面正式打包 -> build/bin/XRUN.exe
```
