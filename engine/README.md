# engine —— 会话引擎使用文档

`github.com/wt68/runcode/engine` 是传输无关的 AI 会话引擎：一次 `Build` 得到一个会话宿主，注入你的流式回调、工具事件通道与审批器，然后逐轮 `RunTurn`。CLI/TUI、XRUN 桌面版与未来的服务端都消费同一个它。本文是**消费者视角的使用指南**；稳定性契约与端口清单见 [docs/engine-api.md](../docs/engine-api.md)，wire 协议见 [docs/protocol.md](../docs/protocol.md)。

## 引入模块

同仓库消费者（根模块已配好 replace + go.work）直接 import。独立仓库消费者：

```
require github.com/wt68/runcode/engine v0.1.0
// 未发布到 proxy 前，指向本地 checkout：
replace github.com/wt68/runcode/engine => ../runcode_desktop/engine
```

典型只需 import ≤6 个包：`engine`（入口）+ 按需 `engine/llm`、`engine/turn`、`engine/tool`、`engine/permissions`、`engine/sessions`。

## 最小可用：跑一轮对话

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wt68/runcode/engine"
)

func main() {
	cfg := engine.Config{
		Provider: "openai",              // "anthropic" | "openai"（含一切 OpenAI 兼容端点）
		Model:    "qwen3-max",
		BaseURL:  "https://your-endpoint/v1", // OpenAI 兼容端点的 API 根
		APIKey:   os.Getenv("MY_KEY"),
		CWD:      `D:\some\workspace`,   // 工具的工作区根；必填
		PermissionMode: "safe",          // safe：非交互，工作区外/危险动作一律拒绝
		PersistSession: true,            // 历史落盘到 <CWD>/.runcode/sessions/
	}
	sess, err := engine.Build(cfg, engine.Options{
		StreamDelta: func(d string) { fmt.Print(d) }, // 助手文本流
	})
	if err != nil {
		panic(err)
	}
	defer sess.Close(context.Background())

	result, err := sess.RunTurn(context.Background(), "看看这个仓库的结构，总结一下")
	if err != nil {
		panic(err)
	}
	fmt.Printf("\n[%d 次迭代，stop=%s]\n", result.Iterations, result.FinalStopReason)
}
```

要点：

- **Config 是纯数据**（一次会话的全部已解析配置），**Options 是行为注入**（回调/接口/通道）。所有 Options 字段可选，零值 Options = 丢弃警告的非交互 safe 会话。
- **零值即默认**：Config 里 0/""/nil 一律表示「引擎默认」，新字段永远遵循此约定（兼容规则见 engine-api.md §2）。
- `Session` 拥有它构建的一切资源（持久化、MCP 连接、后台 shell），**必须 `Close`**。

## Config 速查

| 组 | 字段 |
|---|---|
| 模型接线 | `Provider` `Model` `BaseURL` `APIKey` `AuthToken` `MaxTokens` `MaxRetries` |
| 动态凭证 | `TokenSource func() (string, error)`（每请求取令牌；须 goroutine-safe、绝不返回 `("", nil)`、刷新自行单飞）+ `OnUnauthorized func()`（401 时强制刷新的回调） |
| 会话与持久化 | `CWD` `SessionID` `Resume` `Continue` `PersistSession` `SessionBackend`("jsonl"/"sqlite") `Transcript` `Telemetry` |
| 预算 | `MaxHistoryMessages` `MaxContextTokens`（达 80% 触发语义压缩）`MaxIterations`（ReAct 上限，默认 8） |
| 权限与安全 | `PermissionMode`("safe"/"interactive"/"judge"/"flight") `HarmJudgeModel` `HarmJudgeVotes` |
| 能力扩展 | `MCPServers` `AllowMCPSampling` `Hooks` `DisabledTools/Agents/Skills` |
| 推理 | `Thinking llm.ThinkingConfig` `ReasoningScenario` |
| 提示词 | `SystemPrompt`（整体覆盖）`SystemPromptAppend`（追加） |
| 每会话隔离 | `WebProxy`（联网工具出站代理）`ToolEnv`（工具子进程环境覆盖注入） |
| 计费显示 | `InputPrice` `OutputPrice` `PriceSource` |

## 流式输出与工具事件

```go
toolEvents := make(chan tool.Event, 256) // 你拥有通道生命周期
sess, _ := engine.Build(cfg, engine.Options{
	StreamDelta:    func(d string) { ui.AppendAssistant(d) },
	StreamThinking: func(d string) { ui.AppendThinking(d) }, // 思维链流，可不接
	ToolEvents:     toolEvents,
	Warn:           logWriter,  // 启动期警告（MCP/技能加载失败等），仅 Build 期间写
})
go func() {
	for ev := range toolEvents { ui.RenderToolCard(ev) }
}()
```

- 所有回调在**跑 `RunTurn` 的那个 goroutine** 上同步执行——宿主自己 marshal 回 UI 线程。
- `ToolEvents` 是**非阻塞发送**：通道满则丢事件、绝不阻塞执行器。给足缓冲并及时消费。
- 事件类型：`started/progress/output/completed/failed/agent_delta/agent_usage`（子代理事件经 `ParentToolUseID` 归属到 Task 卡片）。**注意**：`tool.Event` 是引擎内部类型，直接外发给自己进程内的 UI 没问题；要跨 wire 给前端/远端，必须转成 `pkg/protocol.ToolEvent`（转换器在 `internal/host.ToolEventDTO`，规则见 protocol.md §4）。

## 交互审批（interactive / judge 模式）

实现 `permissions.Approver`——执行器深处每次需要授权就调用它并阻塞等答案：

```go
type myApprover struct{}

func (myApprover) Prompt(ctx context.Context, req permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
	// req.Summary（分类摘要）/ req.Targets / req.Command / req.HarmReason / req.SamplingServer
	if userSaysYes(req) {
		return permissions.ApprovalResponse{
			Effect: permissions.EffectAllow,
			Scope:  permissions.ApprovalScopeSession, // Once / Session / Project
		}, nil
	}
	return permissions.ApprovalResponse{
		Effect: permissions.EffectDeny,
		Reason: permissions.ReasonApprovalDenied, // 用户拒绝并停止本轮
	}, nil
}

sess, _ := engine.Build(cfg /* PermissionMode: "interactive" */, engine.Options{
	Approver: myApprover{},
})
```

- **异步 UI 必须保证**：每个 Prompt 最终被 Resolve/拒绝/ctx 取消三者之一解除，绝不悬挂 goroutine。现成的异步审批经纪在 `internal/host.AsyncApprover`（pending 表 + 事件往返），别自己重写。
- 复杂宿主可整个注入 `Options.Permissions`（自建 `permissions.NewService`，接 HarmJudge/熔断/审计），此时 `Approver` 字段被忽略。

## 会话列表、恢复与运行态持久化

```go
backend, _ := sessions.OpenBackend(workspace, sessions.BackendJSONL)
defer backend.Close(ctx)

infos, _ := backend.List(ctx)          // 新→旧：id/时间/turn 数/首尾提示预览
latest, _ := backend.Latest(ctx)

// 恢复：历史 + 运行态（模型/权限模式/计划模式等 SessionMeta）
cfg.Resume = latest
sess, _ := engine.Build(cfg, opts)
blocks := sess.History()               // 重建 UI 渲染
tokens := sess.EstimateContextTokens() // 恢复态的上下文占用估算
```

- 每个 turn 提交即落盘（`Store.Append` 是原子提交单元）；`Close` 同步收尾——**节点 A Close → 节点 B Resume 读到完整历史**，无需额外 flush 协议。
- 运行时开关想随会话持久化（计划模式恢复后不失效），用 `backend.SaveMeta/LoadMeta`，或直接用 `internal/host` 的 setter（已接好）。
- 自定义存储（Redis 热层/DB 归档）：实现 `sessions.Backend` 接口，经 `Options.Backend` 注入（此时你拥有它的生命周期，`Session.Close` 不会关它）；**验收标准 = 通过 `sessions/backendtest.Run`**。

## 常用 Session 方法

| 方法 | 用途 |
|---|---|
| `RunTurn` / `RunTurnWithImages` | 跑一轮（返回 `turn.Result`：最终消息/用量/迭代数/Stopped） |
| `ResetHistory` / `Compact(ctx)` | 清空内存历史 / 立即压缩最旧 turn |
| `SetModel` `SetPermissionMode` `SetPlanMode` `SetThinkingEffort` `SetReasoningScenario` | 运行时开关（线程安全；想持久化用 host 或 SaveMeta） |
| `History` `EstimateContextTokens` `ToolList` `Status` `MCPStatus` | 状态快照 |
| `GenerateTitle(ctx, text)` | 独立请求生成会话标题（不进历史） |
| `ReloadSkills` / `ReloadAgents` | 热重载技能/子代理目录 |
| `Close(ctx)` | 触发 SessionEnd 钩子并关闭全部资源 |

## 并发语义（必读）

- **一个 Session 同时只跑一个 turn**：并发 `RunTurn` 返回错误（拒绝而非排队）。宿主自己串行化提交，或用 `internal/host` 的排队配额。
- 中断在途 turn：取消传给 `RunTurn` 的 ctx。用户「拒绝并停止」则由 Approver 返回 deny 触发（`result.Stopped=true`，消息结构保持良构）。
- **多会话**：一个进程可并发持有任意多个 Session（下层已按会话隔离）。同仓宿主直接用 `internal/host.Manager`——会话表、每会话事件信封（sessionId/seq/ts）、审批路由、同 workspace 后端句柄池、全局配额（`MaxConcurrentTurns`/`MaxGlobalSubagents`/`MaxGlobalBackgroundShells`）、空闲回收，全部现成。
- 跨会话配额也可不经 host 直接注入：`Options.SubagentLimiter`、`Options.ShellBudget`（`bash.NewBudget`）。

## 每会话隔离（多用户/服务端场景）

```go
cfg.WebProxy = userProxy                          // 该会话联网工具的出站代理
cfg.ToolEnv  = map[string]string{"HOME": sandboxHome} // 工具子进程环境覆盖
cfg.TokenSource = thisUser.Token                  // 每会话不同用户的凭证
opts.Backend = redisHotTier                       // 每会话/共享的远程存储
```

引擎无进程级可变全局状态（无 os.Chdir、无全局配置单例；CWD 显式贯穿）——多会话隔离靠**注入不同的 Config/Options**，不靠环境变量。

## 验证你的接入

```
go -C engine test -race ./...        # 引擎自测
go test -race ./你的宿主包            # 宿主侧（host 的 fake 模式可参考 internal/host/fakes_test.go）
make audit                            # 依赖方向审计（engine 不得依赖你的模块）
```
