# 服务端开发交接文档

本文是服务端开发者（独立仓库）的起点。内核（引擎 + 多会话层 + 协议）已定稿并冻结基线；你的工作是在其公开面之上实现服务端外壳与远程存储，**不需要也不应该改引擎内部**。

## 0. 一句话架构

```
你的服务端仓库
  └── require github.com/wt68/runcode/engine （一个模块拿到全部交接面）
        ├── engine          会话引擎（Build/Session，见 engine/README.md 使用指南）
        ├── engine/host     多会话层（会话表/事件信封 seq/审批路由/后端池/配额）
        ├── engine/protocol wire 协议单一事实源（命令注册表/信封/错误码/版本握手）
        └── engine/sessions/backendtest 你的存储实现的验收套件
```

参考实现：`cmd/runcode-server`（本仓库）——可跑的最小服务端骨架（SSE 事件面 + RPC 命令面 + Bearer 桩），**只 import engine 公开面**（有依赖审计测试作证明）。建议直接拷进你的仓库，把 `replace` 换成 `require`，然后按 `// HANDOFF:` 锚点逐个替换。

## 1. 职责矩阵

| 归属 | 内容 |
|---|---|
| **你（服务端）** | HTTP/WS 外壳（骨架的 SSE 可升级 WS）；多用户认证与**每会话** `Config.TokenSource` 注入；**Redis 热层 + DB 归档**的 `sessions.Backend` 实现；（如选中心引擎拓扑）`Options.ToolRuntime` 网关 runtime 与沙盒执行器；部署/伸缩/可观测 |
| **内核侧（我们）** | engine 与 engine/protocol 的演化；协议版本纪律；桌面客户端 |
| **共同** | 协议变更（见 §4 协作规则） |

## 2. 验收标准（每项都有机器判据）

1. **存储**：你的 Backend 实现通过 `engine/sessions/backendtest.Run`（Append 原子性 / LoadHistory 完整性 / SessionMeta 往返 / 重开持久性 / 并发批次不撕裂 / Close 幂等）。归档装饰器契约：**Close 必须先 Flush 归档、再关热层**（`sessions.Backend` 接口文档）。
2. **回合状态机**：SendMessage 接受后 → 0..n 个流事件 → **恰好一个** turn:end / turn:error；回合中重复提交返回 busy。（docs/protocol.md §2；骨架的 httptest 用例可搬去做你的回归。）
3. **信封 seq**：每会话严格 1..n 无缺口、顺序即到达顺序；断线重连缺口检测 → Status + Resume 对账，**不做事件重放**（protocol.md §3）。
4. **审批不悬挂**：每个 permission:request 最终被 Resolve / 中断 DenyAll / 会话关闭三者之一解除（host 已保证；你的传输层不得吞掉 ResolvePermission）。
5. **横向扩展不变式**：turn 内节点亲和；turn 间任意节点可经 LoadHistory + LoadMeta 重建（engine-api.md §3）。热层 write-through 后路由策略（sticky/非 sticky）只是性能选择。

## 3. 禁区

- **不改 `engine/internal/*`**——物理上你也 import 不到；若发现缺口，向内核侧提需求而不是 fork。
- **内部类型不上 wire**——一切跨网络的 payload 必须是 `engine/protocol` 类型；引擎类型经 `engine/host` 的导出转换（`ToolEventDTO` 等）过防腐层。
- **不绕过端口**——存储只经 `Options.Backend`、凭证只经 `TokenSource`、工具环境只经 `ToolEnv`/`WebProxy`/`ToolRuntime`；不要用环境变量做每会话状态（引擎无进程级可变全局，是刻意的）。
- **不删协议字段、不改字段语义**——见 §4。

## 4. 协议协作规则

- **单一事实源在内核仓**：`engine/protocol`（wire 类型 + `CommandKinds` 命令注册表）。共同命令的增改 = 向内核仓提 PR（改 protocol + desktop.App + `go generate ./engine/protocol`），CI 三道门禁（stdlib-only / protogen --check / tsc）是裁判。
- **加字段永远安全**：新字段 optional + `omitempty`；两端必须忽略未知字段与未知枚举值（永不 `DisallowUnknownFields`）。加字段不 bump 版本。
- **删/改语义 = breaking**：走 `Deprecated:` 弃用流程 ≥1 个发布周期，bump `protocol.Version`；握手（`GetProtocolInfo` / 你的 WS 握手）暴露版本，服务端上线后保证兼容 `minSupported ≤ v ≤ current`。
- **服务端专属命令**（管理面/运维面）不进共享协议：在你的仓库自定 namespace（如 `/api/v1/admin/*`），别塞进 `CommandKinds`。

## 5. 建议里程碑

| # | 里程碑 | 验收 |
|---|---|---|
| M1 | 骨架跑通：拷贝 `cmd/runcode-server`，真实 LLM 走通一轮对话（smoke.sh 全绿） | 手动 |
| M2 | 多用户认证 + 每会话 TokenSource；WS 升级（保留 SSE 降级） | busy/401/404 错误码用例 + seq 连续性用例 |
| M3 | Redis 热层 Backend + DB 归档装饰器 | `backendtest.Run` 全绿 + 跨进程 resume 演练（节点 A Close → 节点 B Resume） |
| M4 | 多实例部署 + 会话路由 | 两实例下同会话互斥（路由层保证）、故障转移 resume |
| M5 | （若中心引擎拓扑）ToolRuntime 网关 + 沙盒 | 混合工具集会话端到端；权限/审批语义与本地一致（骨架用例复跑） |

## 6. 起步 checklist

```
1. 拿到引擎：go.mod require github.com/wt68/runcode/engine（tag 见内核仓）；
   私有仓需 GOPRIVATE=github.com/wt68/* 与拉取凭证，或开发期 replace 到本地 checkout。
2. 拷 cmd/runcode-server 进你的仓库，改 module 名，replace→require。
3. 通读三份文档：engine/README.md（怎么用）→ docs/protocol.md（wire 契约）→ docs/engine-api.md（稳定性契约 + §4 工作区触点 + §5 预留设计）。
4. 跑 scripts/smoke.sh 建立基线，然后按 // HANDOFF: 锚点开工。
```
