# runcode (奔跑的代码)

> 一个用 Go 实现的开源终端 AI 编程伴侣。
> English: see [README.md](./README.md)。

[![CI](https://github.com/your-username/runcode/actions/workflows/ci.yml/badge.svg)](https://github.com/your-username/runcode/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/your-username/runcode.svg)](https://pkg.go.dev/github.com/your-username/runcode)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)

> **状态：v0.1-alpha 脚手架。** 二进制能编译能打印帮助，但 `chat` 子命令尚未接通。详见路线图。

## runcode 是什么？

`runcode` 是一个住在终端里的 AI 编程伴侣。以全屏 TUI（Bubble Tea）形式运行，背后是 ReAct + Tool Use 主循环，通过 LLM 提供商（Anthropic Claude 或 OpenAI GPT）驱动，能为你读写、编辑、搜索、执行代码，每一步都有明确的权限门控。

它的灵感来自 Anthropic 的 Claude Code（官方 TS CLI），但是从零用 Go 重新实现核心思想：流式工具执行的并发分组、可缓存的系统提示边界、四级权限模式、生命周期 Hook、MCP 集成、Sub-agent。

## 快速开始

```bash
git clone https://github.com/your-username/runcode.git
cd runcode
go build ./cmd/runcode
./runcode version
./runcode --help
```

> 需要 Go 1.26+。

## 路线图

| 版本 | 周期 | 重点 |
|------|------|------|
| **v0.1** | 4-6 周 | TUI + Anthropic + 7 工具 (Read/Write/Edit/Glob/Grep/Bash/TodoWrite) + 默认权限 + 可缓存提示边界 + SQLite 转录 |
| v0.2 | +3 周 | OpenAI provider + 4 级权限 + Hook 系统 + 斜杠命令 + WebFetch/WebSearch |
| v0.3 | +4 周 | Sub-agent (Explore / Plan / Verification) + 上下文压缩 + Skill + `runcode print` |
| v0.4 | +5 周 | MCP 集成 + Coordinator 多 worker + 插件 manifest |
| v1.0 | +4 周 | 性能打磨 + i18n + GoReleaser 多平台 + Homebrew/scoop/AUR |

完整设计见 [docs/architecture.md](./docs/architecture.md)（待填充）。

## 架构一览

```
用户 → Bubble Tea TUI → REPL 控制器 → ReAct 循环 ─┬─→ LLM Provider (Anthropic | OpenAI)
                            │                     │
                            │           ┌─────────┴─────────┐
                            ↓           │ 流式工具执行器     │
                      权限引擎  ←───────┤ (并发/串行分组)    │
                            │           │                   │
                            ↓           └───────────────────┘
                      Hook 链
```

Go 习惯下的核心抽象映射：

- `AsyncGenerator<Event>` → `chan<- Event` + goroutine
- `DeepImmutable AppState` → `atomic.Pointer[AppState]` + COW
- `useSyncExternalStore` → Store 通知 channel → `tea.Cmd` → `tea.Msg`
- `__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__` → 字符串常量 + Provider 层 `cache_control` 注入
- Feature flag 编译期 DCE → 运行时配置 + interface 注入（单二进制，不上 build tag 矩阵）

## 项目布局

```
cmd/runcode/           CLI 入口 (cobra)
internal/              实现细节（不可被外部 import）
  app/                 Bubble Tea Model/Update/View
  repl/                ReAct 控制器 + 执行器
  permissions/         4 级模式 + 规则引擎
  prompt/              系统提示组装器 + 边界
  hooks/               生命周期 Hook 链
  mcp/                 MCP 连接池
  persistence/         SQLite + RUNCODE.md 加载 + settings
  session/  cost/  telemetry/  ui/
pkg/                   稳定 SDK（承诺语义化版本）
  tool/                Tool 接口 + Context
  llm/                 Provider 抽象 + 中性消息类型
    providers/         anthropic/, openai/
  agent/  skill/  command/  plugin/
tools/                 内置工具实现 + 注册中心
prompts/               //go:embed 模板
```

## 配置

项目级上下文写在仓库根的 `RUNCODE.md`（也兼容读取旧的 `CLAUDE.md`）。具体 schema 在 v0.2 落地。

## 贡献

项目处于 **alpha** 阶段。`pkg/` SDK **在 v1.0 前不稳定**。详见 [CONTRIBUTING.md](./CONTRIBUTING.md)。

## 许可证

Apache-2.0 — 见 [LICENSE](./LICENSE)。

## 致谢

架构概念参考自 Anthropic Claude Code CLI（TypeScript）。所有 Go 代码原创。
