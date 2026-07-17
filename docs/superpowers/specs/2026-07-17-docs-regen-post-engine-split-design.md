# 文档清理与重新生成(引擎独立成仓收尾)设计

日期:2026-07-17
状态:已批准

## 背景与目标

引擎已从本仓库抽出为同级独立仓库 `D:\agentloop`(module `gitlab.ouc-online.com.cn/aibase/agentloop`,首提交 `28dedce`)。本仓库工作区已完成配套切换(未提交):

- 全树 import 由 `github.com/wt68/runcode/engine/...` 批量改为 `gitlab.ouc-online.com.cn/aibase/agentloop/...`;
- 根、`cmd/runcode-desktop`、`cmd/runcode-server` 三个 go.mod 均 `require gitlab.ouc-online.com.cn/aibase/agentloop v0.3.0` + `replace => ../agentloop`(相对路径,要求同级 checkout);
- `go.work` use 列表为 `.`、`./cmd/runcode-desktop`、`./cmd/runcode-server`、`../agentloop`;
- 四个模块(含 `GOWORK=off` 走 replace 链)均编译通过,已实测。

但文档仍描述旧的"三 module 同仓嵌套"架构,且仓内残留一份不可编译的 `engine/` 死副本(import 已被批量改写、自身 module 路径未改)。本次目标:**让本仓库的文档与代码现实一致,并清除死代码**。范围仅限本仓库,不动 `D:\agentloop`。

## 一、删除

| 路径 | 理由 |
|------|------|
| `engine/` 整个目录(含 `engine/README.md`) | 已独立成仓;仓内副本处于不可编译中间态,留着即自相矛盾 |
| `docs/engine-api.md` | 已随引擎复制到 `agentloop/docs/`,双写必漂移 |
| `docs/protocol.md` | 同上 |
| `docs/server-handoff.md` | 同上(`cmd/runcode-server` 代码仍留本仓,交接文档以 agentloop 侧为准) |

## 二、重写(从当前代码生成,外壳仓视角)

原则:本仓库文档只讲"三个外壳如何消费外部引擎 agentloop";引擎内部细节(ReAct 循环、权限系统、工具系统、provider 层、持久化、扩展系统等)一律不再复述,统一链接到 agentloop 仓库的 README 与 docs。

- **`README.md` / `README.zh-CN.md`**:定位改为"runcode——消费 agentloop 引擎的三个外壳:CLI/TUI、XRUN 桌面版、server 交接骨架"。快速开始注明需要 `../agentloop` 同级 checkout。引擎能力只做简述并链接 agentloop 文档。保留 CLI flag 参考、配置文件说明等外壳自有内容,逐项对照当前代码校准。两语言版本内容对齐。
- **`docs/architecture.md`**:重写为外壳架构——本仓三 module(根、desktop、server)+ 外部引擎依赖的边界与依赖方向;`internal/desktop`、`internal/ui`、`pkg/command`、`tools/preview`、`tools/protogen` 职责;CLI 参考;验证命令。引擎内部章节移除,以"引擎内部请见 agentloop"一节收束。
- **`docs/desktop.md`**:校准引擎引用(module 路径、术语、链接),桌面自有内容保留。
- **`CLAUDE.md`**:构建与打包节按新布局重写——三 module 联动命令、`go.work` 含 `../agentloop`、CI/发布 `GOWORK=off` 走 replace 链;删除 engine 嵌套 module 描述、`go -C engine` 命令与旧依赖审计命令。
- **`CHANGELOG.md`**:顶部追加一条"引擎独立成仓"记录,历史条目不动。
- **`CONTRIBUTING.md`**:只校准失效的构建/测试命令。

## 三、联动收尾(让文档里的命令为真)

- **`Makefile`**:`MODULES` 去掉 `engine`;`audit` 目标移除(依赖方向纪律随引擎迁入 agentloop 仓库);`tidy` 覆盖根 + desktop + server;`build`/`test`/`lint`/`fmt` 相应收敛。
- **`.github/workflows/ci.yml`**:删除 engine module 的 lint/test/build/依赖审计/protocol-stdlib 步骤(这些检查属于 agentloop 自己的 CI)。**已知缺口**:root 的 `replace => ../agentloop` 意味着 CI 编译需要 sibling checkout 内网 GitLab 仓库,凭证/镜像属 infra 决策,本次仅在 workflow 注释中标记 TODO,交由维护者决定。

## 四、不动

`docs/superpowers/**`(历史归档)、`CODE_OF_CONDUCT.md`、`SECURITY.md`、`codeql.yml`、`desktop.yml`、`release.yml`(均无 engine 引用)。

## 五、验证口径

全部通过才算完成:

```bash
go build ./...
go -C cmd/runcode-desktop build ./...
go -C cmd/runcode-server build ./...
GOWORK=off go build ./...
GOWORK=off go -C cmd/runcode-server build ./...
go test ./...        # 根模块
make build test      # 新 Makefile
```

另:全文档 grep 兜底——`wt68/runcode/engine`、`go -C engine` 在保留文件中零残留(docs/superpowers 历史归档除外)。
