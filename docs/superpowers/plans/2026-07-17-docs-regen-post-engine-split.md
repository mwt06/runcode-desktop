# 文档清理与重新生成(引擎独立成仓收尾)实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让本仓库的文档、Makefile、CI 与"引擎独立成仓 agentloop"后的代码现实一致,并清除仓内 engine/ 死副本。

**Architecture:** 先把未提交的迁移主体落成一个可编译的 commit,然后逐文件删除/重写/校准,每个 commit 的树都可编译。文档一律按"外壳仓视角"写:本仓库只讲三个外壳如何消费外部引擎,引擎内部细节链接到 agentloop。

**Tech Stack:** Go 1.26 三 module 仓库(根 + cmd/runcode-desktop + cmd/runcode-server)、外部依赖 gitlab.ouc-online.com.cn/aibase/agentloop(同级 checkout `../agentloop` + go.mod replace)、GitHub Actions、GNU Make。

**规格:** `docs/superpowers/specs/2026-07-17-docs-regen-post-engine-split-design.md`(已批准)

## Global Constraints

- 平台:Windows 11,shell 用 Git Bash(POSIX)。git 输出中 "LF will be replaced by CRLF" 警告无害,忽略。
- 除 Task 1 外,每个 commit 只 `git add` 该任务点名的路径,绝不 `git add -A`。
- 每个 commit 后根模块必须可编译:`go build ./...` exit 0。
- 文档语言:`README.md` 英文、`README.zh-CN.md` 中文(内容互为镜像);`CLAUDE.md`、`docs/*.md`、CHANGELOG 条目中文。
- 提交信息结尾加:`Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`。
- `docs/superpowers/**` 是历史归档,允许残留旧引用,所有 grep 兜底都要排除它(以及 `node_modules`、`.git`)。

### 事实表(已实测,写文档时引用,不得凭记忆改写)

| 事实 | 值 |
|------|-----|
| 引擎新仓库 | `gitlab.ouc-online.com.cn/aibase/agentloop`,本机同级 checkout `D:\agentloop`(相对 `../agentloop`),自带 `README.md` 与 `docs/engine-api.md`、`docs/protocol.md`、`docs/server-handoff.md` |
| 根模块 | `github.com/wt68/runcode`;require agentloop v0.3.0;`replace gitlab.ouc-online.com.cn/aibase/agentloop => ../agentloop` |
| 桌面模块 | `github.com/wt68/runcode/cmd/runcode-desktop`;replace agentloop => `../../../agentloop`;另 replace 根模块 => `../..` |
| 服务端模块 | `github.com/wt68/runcode/cmd/runcode-server`;replace agentloop => `../../../agentloop` |
| go.work use | `.`、`./cmd/runcode-desktop`、`./cmd/runcode-server`、`../agentloop` |
| 编译验证(全部 exit 0) | `go build ./...`;`go -C cmd/runcode-desktop build ./...`;`go -C cmd/runcode-server build ./...`;`GOWORK=off go build ./...`;`GOWORK=off go -C cmd/runcode-server build ./...` |
| 迁移后仓库布局 | `cmd/runcode`(Cobra CLI)、`cmd/runcode-desktop`(Wails 外壳+React 前端)、`cmd/runcode-server`(服务端骨架)、`internal/desktop`(桌面核心)、`internal/ui`(Bubble Tea TUI)、`pkg/command`(自定义 slash 命令)、`tools/preview`(桌面产物预览工具)、`tools/protogen`(协议 TS 代码生成器) |
| protogen | `tools/protogen/main.go` 的 `protocolPkgPath = "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"`,生成前端 `src/protocol/*.ts`;CI 用 `go run ./tools/protogen --check` |
| `.golangci.yml` | 无 engine 引用,不用改 |
| `codeql.yml`/`desktop.yml`/`release.yml` | 无 engine 引用,不用改 |

---

### Task 1: 提交迁移主体

工作区里 225 个文件的 import 批量切换 + 三个 go.mod + go.work(+go.work.sum)是迁移主体,尚未提交。先把它落成一个 commit,后续所有工作建立其上。用户已批准。

**Files:** 工作区全部已修改文件(`git add -A`,本任务是唯一例外)。

- [ ] **Step 1: 复验编译**(五条,任何一条非 0 就停下报告,不许带病提交)

```bash
go build ./... && \
go -C cmd/runcode-desktop build ./... && \
go -C cmd/runcode-server build ./... && \
GOWORK=off go build ./... && \
GOWORK=off go -C cmd/runcode-server build ./... && echo ALL-GREEN
```

Expected: `ALL-GREEN`

- [ ] **Step 2: 确认没有意外的未跟踪文件**

```bash
git status --porcelain | grep '^??' || echo NO-UNTRACKED
```

Expected: `NO-UNTRACKED`(若出现未跟踪文件,逐个确认属于迁移再 add,不确定就停下报告)

- [ ] **Step 3: 提交**

```bash
git add -A
git commit -m "refactor: 引擎依赖切换到独立仓库 agentloop

全树 import 由 github.com/wt68/runcode/engine/... 改为
gitlab.ouc-online.com.cn/aibase/agentloop/...;三个 go.mod 均
require agentloop v0.3.0 并 replace => 同级 ../agentloop checkout;
go.work 纳入 ../agentloop。仓内 engine/ 副本下一提交移除。

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: 删除 engine/ 死副本与已搬走文档,修 protogen 陈旧字符串

**Files:**
- Delete: `engine/`(整个目录)、`docs/engine-api.md`、`docs/protocol.md`、`docs/server-handoff.md`
- Modify: `tools/protogen/main.go`(注释与报错字符串仍指向 `engine/protocol`)

- [ ] **Step 1: 删除**

```bash
git rm -r -q -f engine
git rm -q docs/engine-api.md docs/protocol.md docs/server-handoff.md
```

- [ ] **Step 2: 修 protogen 陈旧字符串**。`tools/protogen/main.go` 中:
  - 文件头注释(约 4/6/11 行)把 `engine/protocol` 表述改为 `agentloop 的 protocol 包`,并把"wired to `go generate ./engine/protocol`"改为"直接 `go run ./tools/protogen` 运行";
  - 约 107 行报错字符串 `(run `go generate ./engine/protocol`)` 改为 `(run `go run ./tools/protogen`)`;
  - 约 81 行注释里的 "nested engine module" 表述改为 "agentloop module"。
  改完 `grep -n 'engine' tools/protogen/*.go` 应只剩(如有)与历史无关的匹配;目标是零。

- [ ] **Step 3: 验证编译与生成器自检**

```bash
go build ./... && go run ./tools/protogen --check && echo GREEN
```

Expected: `GREEN`(protogen --check 通过说明删除 engine/ 不影响协议生成链)

- [ ] **Step 4: 提交**

```bash
git add tools/protogen/main.go
git commit -m "chore: 移除仓内 engine/ 副本与已随引擎迁走的三篇文档

引擎已独立成仓 agentloop;仓内副本 import 已被批量改写、module 路径
未改,处于不可编译中间态,留着即自相矛盾。docs/engine-api.md、
docs/protocol.md、docs/server-handoff.md 已复制到 agentloop/docs,
以彼侧为准。protogen 的注释/报错字符串不再指向已删除路径。

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: Makefile 收敛

**Files:**
- Modify: `Makefile`(整文件替换为下面内容)

**Interfaces:** Produces:`make build`/`make test`/`make lint`/`make fmt`/`make tidy` 覆盖根+server 两 module(desktop 的 Go 侧编译并入 build);`audit` 目标删除(依赖方向纪律随引擎迁入 agentloop 仓库自己的 CI)。后续 CLAUDE.md/CONTRIBUTING 的命令claims 必须与此一致。

- [ ] **Step 1: 整文件替换 Makefile**

```makefile
APP := runcode
MAIN := ./cmd/runcode
BIN_DIR := bin

# Three Go modules share this repo: the root (CLI/TUI + desktop core), the
# server skeleton, and the Wails desktop shell. The engine lives in its own
# repository (gitlab.ouc-online.com.cn/aibase/agentloop), consumed through
# each go.mod's replace pointing at a sibling checkout ../agentloop. The
# desktop shell packages via `wails build`; here it only gets a Go-side
# compile check.
MODULES := . cmd/runcode-server

.PHONY: all build run test lint fmt tidy clean snapshot

all: build

build:
	@mkdir -p $(BIN_DIR)
	go -C cmd/runcode-server build ./...
	go -C cmd/runcode-desktop build ./...
	go build -trimpath -o $(BIN_DIR)/$(APP) $(MAIN)

run:
	go run $(MAIN) chat

test:
	@for m in $(MODULES); do go -C $$m test -race ./... || exit 1; done

lint:
	@for m in $(MODULES); do (cd $$m && golangci-lint run ./...) || exit 1; done

fmt:
	@for m in $(MODULES); do gofmt -w $$(go -C $$m list -f '{{.Dir}}' ./...); done

tidy:
	@for m in $(MODULES); do go -C $$m mod tidy || exit 1; done
	go -C cmd/runcode-desktop mod tidy

snapshot:
	goreleaser build --snapshot --clean

clean:
	rm -rf $(BIN_DIR) dist coverage.out coverage.html
```

- [ ] **Step 2: 验证**

```bash
make build && echo BUILD-OK && make -n test lint | head -5
```

Expected: `BUILD-OK`,dry-run 输出的循环命令只涉及 `.` 与 `cmd/runcode-server`。

- [ ] **Step 3: 提交**

```bash
git add Makefile
git commit -m "build: Makefile 收敛到外壳三 module(引擎目标随独立成仓移除)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: CI workflow 调整

**Files:**
- Modify: `.github/workflows/ci.yml`

变更清单(对照当前文件):
1. `env:` 注释块后追加 **TODO 头注释**(原文如下,放在 `env: GOWORK: off` 之前):

```yaml
# TODO(agentloop checkout): every job below needs the engine repository
# (gitlab.ouc-online.com.cn/aibase/agentloop) checked out at ../agentloop —
# each go.mod's replace points there. Wiring that up (deploy key / mirror /
# GOPRIVATE + credentials) is an infra decision still to be made; until it
# lands these jobs cannot resolve the engine module.
```

2. `lint` job:删除 "golangci-lint (engine)" step(带 `working-directory: engine` 的那个)。
3. `test` job:删除 "Test (engine module)" step(`go -C engine test -race ./...`)。
4. `build` job:删除 "Build (engine module)" step(`go -C engine build ./...`)。
5. 删除整个 `deps` job("Dependency direction audit"——引擎不依赖外壳的审计已随引擎迁入 agentloop 仓库自己的 CI)。
6. `protocol` job:删除 "Protocol package is stdlib-only" step(`go -C engine list ... ./protocol`——同样属于 agentloop CI);保留 "Generated protocol is fresh"(`go run ./tools/protogen --check`)与 "Frontend typecheck"。

- [ ] **Step 1: 按清单编辑 ci.yml**(用 Edit 工具逐处修改,不整文件重写,保留其余内容原样)

- [ ] **Step 2: 验证无 engine 残留且 YAML 结构完好**

```bash
grep -n 'engine' .github/workflows/ci.yml; python -c "import yaml,sys; yaml.safe_load(open('.github/workflows/ci.yml',encoding='utf-8')); print('YAML-OK')" 2>/dev/null || npx --yes yaml-lint .github/workflows/ci.yml 2>/dev/null || echo '(无本地 yaml 校验器,人工复读一遍缩进)'
```

Expected: grep 只命中 TODO 注释里的 "engine repository" 字样;YAML 校验通过(或人工复读确认)。

- [ ] **Step 3: 提交**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: 移除 engine module 步骤(lint/test/build/依赖审计/protocol stdlib 门禁随引擎迁入 agentloop CI)

CI 如何拿到内网 agentloop 依赖(deploy key/镜像/GOPRIVATE)是待定
infra 决策,见文件顶部 TODO;在那之前这些 job 无法解析引擎模块。

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: 重写 CLAUDE.md

**Files:**
- Modify: `CLAUDE.md`(整文件替换)

要求:「协作原则」一节原样保留;「构建与打包」按新布局重写。整文件替换为:

```markdown
# CLAUDE.md

## 协作原则

- 不要机械追求“最小改动”。实现方案应优先保证系统的可靠稳定、可扩展、性能可控、高内聚、低耦合。
- 对简单 bug 或明确小需求，可以保持小范围修改；但如果“最小补丁”会造成脆弱设计、重复逻辑、抽象泄漏、后续难扩展或性能隐患，应主动提出并实现更稳健的基础层。
- 规划功能时要说明取舍：哪些是当前必须做的可靠性/扩展性基础，哪些是暂不做的过度工程。
- 默认避免无关扩张，但不要为了少改几行牺牲长期架构质量。

## 构建与打包

引擎已独立成仓：**`gitlab.ouc-online.com.cn/aibase/agentloop`**，以同级 checkout `../agentloop` 存在，经各 go.mod 的 `replace` 指向。改引擎（ReAct 循环、工具、权限、provider、持久化等）去 agentloop 仓库；本仓库只改外壳。

本仓库含**三个 Go module**（依赖方向：外壳 → agentloop，反向不存在）：

1. **根模块（`github.com/wt68/runcode`）**——CLI/TUI（`cmd/runcode`、`internal/ui`、`pkg/command`）+ 桌面核心（`internal/desktop`）+ `tools/preview`、`tools/protogen`。
2. **桌面外壳（`cmd/runcode-desktop`，嵌套 module）**——Wails/CGO 重依赖隔离层。
3. **服务端骨架（`cmd/runcode-server`，嵌套 module）**——独立仓库服务端的可跑参考实现。

`go.work` 已提交（use：`.`、`./cmd/runcode-desktop`、`./cmd/runcode-server`、`../agentloop`）供本地联动；**CI/发布链路一律 `GOWORK=off`**，go.mod 的 replace 链是权威（三个 go.mod 各自 replace 指向 `../agentloop`；CI 目前尚未解决内网 agentloop 的拉取，见 `.github/workflows/ci.yml` 顶部 TODO）。

### 常用命令（根目录执行）

- 全量编译：`go build ./... ; go -C cmd/runcode-server build ./... ; go -C cmd/runcode-desktop build ./...`（或 `make build`）。
- 测试（CI 用 `-race`，三平台）：`go test -race ./... ; go -C cmd/runcode-server test -race ./...`（或 `make test`）。
- Lint：`golangci-lint run`（或 `make lint`；配置根 `.golangci.yml`，启用 gosec/errcheck/gocritic）。
- 协议 TS 再生成（引擎 protocol 变更后）：`go run ./tools/protogen`；CI 用 `--check` 防漂移。
- 出 CLI 二进制：`go build -o runcode.exe ./cmd/runcode`。

### 桌面版（Wails，`cmd/runcode-desktop`）

- 桌面是**嵌套 Go module**（`cmd/runcode-desktop/go.mod`），用 `replace github.com/wt68/runcode => ../..` 指回核心、`replace ...agentloop => ../../../agentloop` 指向引擎，把 Wails/CGO/WebView 重依赖隔离在核心之外；核心的 `go build ./...` 与 CI 不会拉 Wails。模块路径仍在 `github.com/wt68/runcode/...` 下，故可复用核心的 `internal/` 包。
- **正式打包**（产出可发布 exe，需已装 `wails` CLI，本机验证 v2.12.0；会跑 `npm install` + `npm run build` 重建前端）：
  ```bash
  cd cmd/runcode-desktop && wails build
  ```
  产物：`cmd/runcode-desktop/build/bin/XRUN.exe`（应用名/输出名 `XRUN` 来自 `wails.json` 的 `outputfilename`）。
- **仅 Go 侧快速编译检查**（不打包、不重建前端）：`go -C cmd/runcode-desktop build ./...`。
- 跨平台：Wails **不能交叉编译**（各 OS WebView 不同——Windows WebView2 / Linux WebKitGTK / macOS WKWebView），需在目标平台原生构建；CI 见 `.github/workflows/desktop.yml`（Linux 加 `-tags webkit2_41 -clean`）。
- `*.exe`（`XRUN.exe`、根目录的 `runcode-desktop.exe` 等）是 `.gitignore` 的构建产物，不进版本库。
```

- [ ] **Step 1: 整文件替换 CLAUDE.md 为上述内容**
- [ ] **Step 2: 校验命令为真**

```bash
go build ./... && go -C cmd/runcode-server build ./... && go -C cmd/runcode-desktop build ./... && echo CMDS-TRUE
```

Expected: `CMDS-TRUE`

- [ ] **Step 3: 提交**

```bash
git add CLAUDE.md
git commit -m "docs: CLAUDE.md 构建指引按引擎独立成仓后的三外壳布局重写

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 6: 重写 docs/architecture.md(外壳仓视角)

**Files:**
- Modify: `docs/architecture.md`(整文件重写)

**写作要求**(实施者先读代码核实每个 claim,再落笔;禁止从旧版复制引擎内部章节):

结构与内容(中文,预计 100–140 行):

1. **标题+导语**:本文描述 runcode 仓库(引擎的三个外壳)的分层与边界;引擎内部见 agentloop 仓库文档;桌面细节见 docs/desktop.md。
2. **总览:三个外壳,一个外部引擎**——ASCII 图:cmd/runcode chat / cmd/runcode tui / XRUN 桌面(internal/desktop + Wails 前端)/ cmd/runcode-server,汇聚到外部 module `gitlab.ouc-online.com.cn/aibase/agentloop`(engine.Build → *engine.Session);一句话:引擎内部不含 UI 逻辑,外壳注入回调/事件通道/审批器。
3. **模块边界(依赖方向)**——三 module + 外部 agentloop;依赖方向:外壳 → agentloop;go.work 供本地联动、CI `GOWORK=off`、replace 链权威(引用事实表的 replace 路径);"引擎永不依赖外壳"的纪律由 agentloop 仓库自己的 CI 审计。
4. **仓库布局**——事实表"迁移后仓库布局"那 8 项,每项一行职责说明。逐项 `ls` 核实存在。
5. **外壳如何消费引擎**——读 `cmd/runcode/chat.go`、`internal/ui`、`internal/desktop/app.go`、`cmd/runcode-server/server.go` 核实后,各写 2-4 行:CLI/TUI 直接 engine.Build;桌面 desktop.App 作为 agentloop/host.Manager 上的薄适配层;server 骨架依赖 engine 公开面 + host。写明各自注入什么(流式回调、工具事件、审批器、harm judge 等——以代码为准)。
6. **协议与代码生成**——tools/protogen 读 agentloop/protocol 生成前端 `src/protocol/*.ts`;CI `go run ./tools/protogen --check` 防漂移;协议详情链接 agentloop `docs/protocol.md`。
7. **CLI 参考**——沿用旧版"CLI 参考"节(七个子命令 + flag/env 表 + TUI slash 命令),逐条对照 `cmd/runcode` 代码核实后保留;此节是外壳自有内容。
8. **引擎内部请见 agentloop**——列表链接:agentloop `README.md`(消费者指南)、`docs/engine-api.md`(门面 API)、`docs/protocol.md`(wire 协议)、`docs/server-handoff.md`(服务端交接);注明 ReAct 循环、权限系统、工具系统、provider、持久化、MCP/skills/子代理/记忆/hooks 的文档都在彼侧。
9. **验证**——bash 代码块:

```bash
go build ./...                          # 根模块全量编译
go test -race ./...                     # CI 同款
go -C cmd/runcode-server build ./...    # 服务端骨架
go -C cmd/runcode-desktop build ./...   # 桌面 Go 侧快速检查
cd cmd/runcode-desktop && wails build   # 桌面正式打包 -> build/bin/XRUN.exe
```

- [ ] **Step 1: 按上述结构核实代码并重写 docs/architecture.md**
- [ ] **Step 2: 自检**——文中不得出现 `engine/`(指仓内目录)、`github.com/wt68/runcode/engine`、`go -C engine`;`grep -n 'wt68/runcode/engine\|go -C engine' docs/architecture.md` 为空。
- [ ] **Step 3: 提交**

```bash
git add docs/architecture.md
git commit -m "docs: architecture.md 重写为外壳仓视角(引擎内部指向 agentloop)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 7: 校准 docs/desktop.md

**Files:**
- Modify: `docs/desktop.md`(点状校准,不重写)

变更清单:
1. 第 5 段布局图(约 22 行)中 `engine/（嵌套 module）         共享引擎门面（CLI/TUI/桌面三前端共用，见 docs/architecture.md）` 一行改为引擎为外部依赖的表述,如:`（引擎为外部 module gitlab.ouc-online.com.cn/aibase/agentloop，同级 checkout ../agentloop，见 docs/architecture.md）`。
2. 约 83 行嵌套 module 说明补上 agentloop replace:`replace github.com/wt68/runcode => ../..` 之后加 `、replace ...agentloop => ../../../agentloop`。
3. `grep -n 'engine' docs/desktop.md` 逐个检查其余命中:凡指"仓内 engine module"的表述改为 agentloop;泛指"引擎"的中文表述保留。
4. 其余内容(命令面/事件面/审批/前端/构建/缺口)不动——但如果第 5 步核实中发现与代码明显不符的 claim,顺手修正并在 commit message 里注明。

- [ ] **Step 1: 按清单校准**
- [ ] **Step 2: 自检**——`grep -n 'wt68/runcode/engine\|嵌套 module）\s*共享引擎' docs/desktop.md` 为空。
- [ ] **Step 3: 提交**

```bash
git add docs/desktop.md
git commit -m "docs: desktop.md 校准引擎引用(外部 agentloop 依赖)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 8: 重写 README.md 与 README.zh-CN.md

**Files:**
- Modify: `README.md`、`README.zh-CN.md`(结构性重写;两文件内容互为镜像,先写英文再译)

**写作要求**(实施者对照当前代码核实每个 claim):

保留:标题、badges、License、Acknowledgements、Contributing 指引。按下列结构重组(预计每份 120–160 行,现 225 行):

1. **状态段**:改为"Three shells over one engine:a shell-friendly `chat` CLI, a Bubble Tea `tui`, a Wails desktop app (**XRUN**), and a server skeleton. The engine lives in its own repository — `gitlab.ouc-online.com.cn/aibase/agentloop`."(中文版对应译)。
2. **What is runcode**:两段——本仓库是外壳集;引擎(ReAct 循环、14 内置工具、MCP/skills/子代理/记忆/hooks、四权限模式+harm gate、会话持久化、压缩)一句带过并链接 agentloop。删除 `internal/engine` 门面等陈旧表述。
3. **Quick start**:更新为双仓 clone(同级):

```bash
git clone https://github.com/wt68/runcode.git
git clone <agentloop-remote> agentloop   # engine repo, must sit next to runcode/
cd runcode
go build ./cmd/runcode
./runcode version
```

   注:`<agentloop-remote>` 写 `gitlab.ouc-online.com.cn/aibase/agentloop` 的 https 形式;加一句"module 经各 go.mod `replace => ../agentloop` 解析,agentloop 目录名与位置固定"。
4. **Current CLI**:保留 chat/tui 用法示例与 flag/env 列表——逐条对照 `cmd/runcode` 的 flag 定义核实(有 `--permission-mode` 等新增枚举变化则更新)。
5. **Engine features(压缩)**:原 Configuration files / MCP servers / Skills / Sub-agents / Memory / Hooks / Session resume & compaction / Implemented tools / Permissions and safety 九节(~190 行)压缩为一节"Features (engine)":每项 2-4 行行为概述 + 配置入口(如 MCP 只在用户级 config.toml、hooks 8 事件、memory 两 scope、会话 `--resume/--continue`、权限 safe/interactive[CLI] + judge/flight[桌面]),细节链接 agentloop README/docs。**CLI 用户可见的操作命令保留**:`runcode sessions|transcript|permissions|config` 一览。
6. **Architecture at a glance**:新 ASCII 流(用户输入 → cmd/runcode chat|tui → agentloop engine.Build/Session → provider/tools/permissions → 输出),链接 docs/architecture.md、docs/desktop.md 与 agentloop。
7. **Project layout**:按事实表 8 项重写(旧版列的 internal/repl、internal/permissions、pkg/tool 等早已不存在,全部清除)。
8. **README.zh-CN.md**:同结构中文镜像;首行互链保留。

- [ ] **Step 1: 重写 README.md**
- [ ] **Step 2: 重写 README.zh-CN.md(镜像)**
- [ ] **Step 3: 自检**

```bash
grep -n 'internal/engine\|internal/repl\|internal/permissions\|pkg/tool\|pkg/llm\|wt68/runcode/engine' README.md README.zh-CN.md || echo CLEAN
```

Expected: `CLEAN`

- [ ] **Step 4: 提交**

```bash
git add README.md README.zh-CN.md
git commit -m "docs: README(双语)按三外壳+外部 agentloop 引擎重写

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 9: CHANGELOG 追加 + CONTRIBUTING 校准

**Files:**
- Modify: `CHANGELOG.md`(只在 `## [Unreleased]` → `### Changed` 列表顶部插入一条,历史条目不动)
- Modify: `CONTRIBUTING.md`(点状校准)

- [ ] **Step 1: CHANGELOG 插入条目**(作为 `### Changed` 第一条):

```markdown
- **引擎独立成仓**：嵌套 module `engine/` 迁出为独立仓库 `gitlab.ouc-online.com.cn/aibase/agentloop`，本仓库经各 go.mod 的 `replace => ../agentloop`（同级 checkout）消费，`go.work` 已含该路径。仓内 `engine/` 副本与随引擎迁移的 `docs/engine-api.md`、`docs/protocol.md`、`docs/server-handoff.md` 移除，引擎文档以 agentloop 仓库为准。本仓库收敛为三个外壳：CLI/TUI（根模块）、XRUN 桌面（`cmd/runcode-desktop`）、服务端骨架（`cmd/runcode-server`）。
```

- [ ] **Step 2: CONTRIBUTING 校准**:
  1. Quick development setup 的 clone 段改为双仓 clone(与 README Quick start 一致的形式);
  2. Architectural rules 一节:`pkg/` 公开面、`tools/<name>/` 实现 `pkg/tool.Tool`、`pkg/llm/providers/<name>/` 三条已失实(这些包早已迁入引擎)——改为两条:`internal/` 不对外导出;工具/provider/引擎公开面的贡献去 agentloop 仓库,本仓库贡献集中在外壳(CLI/TUI/桌面/server)。
  3. 其余(PR 流程、bug 报告、license)不动。

- [ ] **Step 3: 提交**

```bash
git add CHANGELOG.md CONTRIBUTING.md
git commit -m "docs: CHANGELOG 记引擎独立成仓;CONTRIBUTING 校准双仓开发与架构规则

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 10: 终验

- [ ] **Step 1: 全量构建+测试**

```bash
go build ./... && \
go -C cmd/runcode-desktop build ./... && \
GOWORK=off go build ./... && \
GOWORK=off go -C cmd/runcode-server build ./... && \
make test && echo FINAL-GREEN
```

Expected: `FINAL-GREEN`(make test 跑根+server 全量 -race,耗时数分钟属正常)

- [ ] **Step 2: grep 兜底**(全部应只输出 CLEAN)

```bash
grep -rn 'wt68/runcode/engine\|go -C engine' \
  --include='*.md' --include='*.go' --include='Makefile' --include='*.yml' . \
  | grep -v docs/superpowers | grep -v node_modules || echo CLEAN-1
grep -rn 'docs/engine-api\.md\|docs/protocol\.md\|docs/server-handoff\.md' \
  --include='*.md' . \
  | grep -v docs/superpowers | grep -v node_modules | grep -v CHANGELOG.md \
  | grep -v agentloop || echo CLEAN-2   # 经 agentloop 指向彼侧文档的链接是合法引用
ls engine 2>/dev/null || echo CLEAN-3
```

Expected: `CLEAN-1`、`CLEAN-2`、`CLEAN-3`。任何残留:回到对应任务修掉再重跑。

- [ ] **Step 3: 汇报**——列出本次全部 commit(`git log --oneline <迁移主体commit>^..HEAD`)、验证输出、以及遗留 TODO(CI 的 agentloop checkout infra 决策)。
