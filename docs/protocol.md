# runcode 双端协议规范

本文档与 `engine/protocol`（wire 类型的单一事实源）配套，定义**传输无关的协议语义**：回合状态机、事件信封、错误模型、版本化与兼容规则，以及同一协议在两种传输（Wails 进程内绑定 / 未来 HTTP+WebSocket）上的映射。改协议 = 改 `engine/protocol` + `go generate`（protogen 生成前端 `src/protocol/*.ts`，CI 三道门禁防漂移）。

## 1. 协议模型

- **命令面**：请求/响应，同步语义。命令形状的单一事实源是 `internal/desktop.App` 的导出方法签名（wire 类型全部来自 `engine/protocol`）；每条命令在 `protocol.CommandKinds` 登记幂等类别：
  - `query`——只读幂等（List*/Read*/Get*/Status/...）；
  - `idempotent-set`——重复同参调用收敛（Set*/Resolve*/Save*/Delete*/...）；
  - `trigger`——非幂等（SendMessage/StartSession/Compact/Import*/...），跨网络传输重试需客户端去重（未来 WS 信封的 client request id 承担，不进 DTO）。
- **事件面**：宿主推送，**每会话内全序**（由 envelopeSink 的单临界区发射保证，协议将其固化为承诺）。

## 2. 回合状态机（核心契约）

```
SendMessage 被接受（返回 nil）
  → 0..n 个 assistant:delta | assistant:thinking | tool:event
             | permission:request | harm:autoallow | warning
  → 恰好一个 turn:end 或 turn:error
```

- 回合进行中再次 SendMessage → `busy` 错误（引擎 turnActive 门是正确性防线）。
- `permission:request` 阻塞对应工具直到 ResolvePermission(id, decision)；回合被中断或会话关闭时，未决请求一律按 deny 解除（fail-closed，绝不悬挂）。
- 两种传输都必须履行同一状态机；前端状态管理只依赖它。

## 3. 事件信封

每个事件的 wire 形态是 `protocol.Envelope`：

```json
{ "event": "tool:event", "sessionId": "sess_x", "seq": 42,
  "ts": "2026-07-16T20:01:02.345Z", "payload": { ... } }
```

- `seq` 每会话单调递增从 1 起，会话重建时归零。**发射端保证 seq 顺序即到达顺序**（seq 分配与下游发射在同一临界区）。重连客户端靠 seq 检测缺口 → 走 `Status` + `ResumeSession` 对账；事件**不重放**。
- `ts` 是唯一的事件时钟（RFC3339，亚秒精度）；payload 内不再携带时间字段。
- `sessionId` 为空表示进程级事件（如会话开始前的 `passport:changed`）。
- 会话寻址放信封/传输层，**不进请求 DTO**——多会话化只改信封与路由，不动 67 个命令的形状。

## 4. 内部类型防腐

引擎内部类型（`engine/tool.Event`、`engine/permissions.ApprovalSummary`、...）**永不直接上 wire**。`internal/desktop/protocol_convert.go` 是唯一转换关卡：引擎侧演化不会隐式改变 wire；要扩展 wire 必须显式改 `engine/protocol` + 转换函数。`engine/protocol` 的 import 仅限 stdlib（结构性强制，protogen/CI 校验）。

## 5. 错误模型

命令失败序列化为 `protocol.Error{code, message, details?}`；错误码见 `protocol/errors.go`（`no_session`/`busy`/`invalid_argument`/`not_found`/`not_logged_in`/`unavailable`/`internal`）。客户端收到无法解析为 Error 的普通字符串时，必须包装为 `internal` 码而非报错（宿主逐步采纳结构化错误的过渡语义）。

## 6. 版本化与兼容规则

- `protocol.Version` 单调整数，经握手交换（桌面 `GetProtocolInfo` 命令；未来服务端在 WS 握手/`GET /api/v1/protocol`），**不进每条信封**。
- **加字段永远安全**：新字段必须 optional + `omitempty`；两侧必须忽略未知字段（Go 端永不使用 `DisallowUnknownFields`；TS 天然忽略）。加字段不 bump 版本。
- **未知枚举值降级**：客户端遇到未知 `ToolEvent.type`、事件名、错误码时必须降级渲染（生成的联合类型带 `(string & {})` 逃逸口），绝不报错。
- **删/改字段走弃用流程**：Go 注释 `Deprecated:` → 双端停止读取但继续发送 ≥1 个发布周期 → 删除并 bump Version。
- **事件语义变更**（回合状态机改动）视同 breaking，必须 bump Version；不允许"同名不同义"。
- 偏斜政策：桌面前后端同包发布零偏斜，v1 冻结前允许直接 breaking；服务端客户端出现起，宿主保证兼容 `minSupported ≤ v ≤ current`。

## 7. 传输映射

| 协议概念 | Wails（现状） | HTTP/WS（未来服务端） |
|---|---|---|
| 命令调用 | `window.go.desktop.App.X(args)` → Promise | `POST /api/v1/rpc/{Command}` 或 WS `{id, method, params}` |
| 命令成功 | resolve(响应 DTO) | 200 + DTO / `{id, result}` |
| 命令失败 | reject(string)——客户端按 §5 解析 | 4xx/5xx + `protocol.Error` / `{id, error}` |
| 事件 | `EventsOn(name, Envelope)` | WS 推送 Envelope（单连接多路复用），降级 SSE |
| 会话寻址 | 隐式（单会话） | sessionID 进 URL/RPC 信封 |
| 断线恢复 | 不存在 | seq 缺口 → Status + ResumeSession 对账 |
| trigger 去重 | 不需要 | WS 信封 client request id（届时定义） |

## 8. 漂移门禁（CI）

1. **生成物新鲜度**：`go run ./tools/protogen --check`——改了 protocol 忘了生成 → 红。
2. **命令清单交叉校验**（protogen 内建）：`desktop.App` 导出方法 ↔ `protocol.CommandKinds` 双向一致；方法签名出现非 protocol 类型 → 红。
3. **前端类型门禁**：`npm run typecheck`（`tsc --noEmit`）。
