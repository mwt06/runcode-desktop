# 设计:AI 规划任务进度看板(右侧常驻面板)

- 日期:2026-07-01
- 状态:已确认设计,待实现
- 范围:runcode 桌面版(`cmd/runcode-desktop` 前端 + `internal/desktop` / `tools/todo` 后端)

## 1. 背景与目标

模型在多步任务中通过 `TodoWrite` 工具(UI 中文名「规划任务」)记录并更新任务清单。当前该清单在前端只作为**普通工具行**混在 Read/Edit/Bash 等执行行里(`App.tsx` 的 `ExecutionCard`),且后端只把一个**摘要字符串**(`"todos 3/5: 修改门面"`)发给前端,结构化清单没有送达前端。

**目标**:把 AI 的 TodoWrite 计划从工具行噪音中提出来,做成一个**右侧常驻、实时刷新、只读**的任务进度看板,让用户随时看到「AI 现在在做什么、进度到哪」。

## 2. 数据来源与范围(已确认)

- **数据来源**:AI 每轮用 `TodoWrite` 生成/更新的任务清单。**只读**,用户不手动增删勾选。
- **展示位置**:消息流右侧常驻面板(right rail)。
- **展示形式**:竖向清单 + 顶部总进度条(保留 AI 给出的原始顺序,同时只有一项「进行中」)。
- **空闲行为**:有计划时自动滑出;全部完成 / 新一轮无计划时自动收成右缘细条,消息流回收宽度;细条可点击随时展开。

## 3. 架构设计

核心取舍:**复用 `pkg/tool` 已存在但未使用的 `Event.Data any` 字段**承载结构化清单,不新增事件类型、不改事件管线。改动集中在 `tools/todo` 一处 + 前端一个新组件。高内聚、低耦合、可扩展。

### 3.1 数据链路

`tools/todo/todo.go` 的 `emitTodoEvent` 目前仅发 `Message`。改为同时在 `Event.Data` 上挂结构化快照:

```go
// 结构化快照类型(定义在 todo 包内)
type todoSnapshot struct {
    Items []item `json:"items"` // content / status / activeForm
    Done  int    `json:"done"`
    Total int    `json:"total"`
}

// emitTodoEvent 内:
out <- tool.Event{
    Type:    tool.EventTypeProgress,
    ToolName: "TodoWrite",
    Message: message,                 // 保留,兼容既有 UI 与旧行为
    Data:    todoSnapshot{...},        // 新增:完整清单
}
```

- `internal/desktop` 的 `EventToolEvent` 原样透传整个 `tool.Event`,`Data` 字段自动随 JSON 序列化到达前端,**无需改 desktop 层**。
- `Data` 是 in-process UI 专用字段(与 `Input`/`Image` 一致),不落 telemetry / transcript,不影响持久化。

### 3.2 前端状态与组件

- `bridge.ts`:
  - `ToolEvent` 增加可选 `data?: unknown`(live 事件为对象)。
  - 新增类型 `PlanItem { content; status; activeForm? }` 与 `PlanSnapshot { items; done; total }`。
- `App.tsx`:
  - 新增状态 `const [plan, setPlan] = useState<PlanSnapshot | null>(null)`。
  - 在 `onEvent(Events.ToolEvent)` 回调中:当 `ev.toolName === 'TodoWrite'` 且带结构化 `data` 时,解析并 `setPlan(...)`。解析容忍 live 对象形态(与现有 `toolInputObj` / `analyzeSteps` 同风格)。
  - 新增 `<PlanPanel plan={plan} .../>` 组件作为右侧栏本体,渲染在 `main` 布局右侧。
  - **在 `groupBlocks` 里把 `TodoWrite` 从 exec 列表隐去**(与 `AskUser` / `Analyze` 不进 exec 组同理):看板成为计划的唯一真源,消息流中不再重复出现「规划任务」行。

### 3.3 布局与交互

```
展开态(有进行中的计划)          收起态(空闲 / 全部完成)
┌────┬──────────┬─────────┐    ┌────┬──────────────┬─┐
│侧栏│ header    │📋任务进度│    │侧栏│ header        │📋│
│    ├──────────┤▓▓▓▓░ 3/5│    │    ├──────────────┤3 │
│会话│  消息流   │✓ 读配置  │    │会话│   消息流       │/ │
│    │          │⟳ 改门面  │    │    │  (回收宽度)    │5 │
│    ├──────────┤○ 写测试  │    │    ├──────────────┤▓ │
│    │  输入框   │○ 跑测试  │    │    │   输入框       │  │
└────┴──────────┴─────────┘    └────┴──────────────┴─┘
   面板宽 ~280px                  细条宽 ~40px,点开还原
```

- **展开触发**:出现计划、或计划有更新(有一项处于 `in_progress`)→ 自动滑出为 ~280px 面板。
- **收起触发**:计划全部完成、或新一轮对话没有计划 → 自动收成 ~40px 右缘细条(📋 图标 + `done/total` 迷你进度)。
- **手动控制**:细条可点击展开;展开态右上角提供折叠按钮手动收起。用户手动折叠后,同一份计划不再自动弹出(避免打扰),下一份新计划恢复自动行为。
- **动画**:CSS `transition` 做宽度滑动,避免消息流布局跳动。

### 3.4 视觉细节(沿用现有主题变量)

- 顶部 `📋 任务进度` 标题 + 总进度条(复用 `ContextMeter` 的进度条样式与配色)。
- 每项一行:状态图标 + 文案。
  - `✓ 已完成`:muted / faint 弱化,可加删除线感的降饱和。
  - `⟳ 进行中`:primary 高亮,文案优先用 `activeForm`(现在进行时),左侧一条 primary 竖条强调(与现有卡片强调风格一致)。
  - `○ 待办`:muted。
- 面板背景 / 边框沿用 `bg-surface` / `border-line2`,与侧栏、卡片一致。

## 4. 详细行为规格

| 场景 | 行为 |
|---|---|
| 首次出现计划 | 面板从细条滑出为展开态 |
| 计划更新(某项转 in_progress / completed) | 面板原地更新,进度条与高亮项随之变化 |
| 全部完成(done == total) | 顶部显示「全部完成 done/total」,本轮结束后自动收成细条(保留最后一份,可从细条重看) |
| 新一轮无 TodoWrite | 保留上一份计划于细条;不自动弹出 |
| 用户手动折叠 | 收成细条并抑制本份计划的自动弹出 |
| 恢复历史会话 | 面板从空(细条隐藏)开始,下一次 TodoWrite 再填充(见非目标) |

## 5. 非目标 / 取舍(明确不做)

- **只读**:不支持用户手动增删 / 勾选任务。
- **单份最新计划**:TodoWrite 每次整表替换,面板只跟踪最新快照;不做多份计划历史留存。
- **子代理(Task)的 todo 不并入主看板**:主看板只显示主 agent 计划,子代理 todo 仍在各自嵌套卡中(维持现状)。
- **跨恢复重建计划状态**:结构化 todo 目前不落盘;v1 恢复会话后看板从空开始。列为二期(需在 transcript / session 侧持久化最后一份 todo 快照)。

## 6. 涉及文件

| 文件 | 改动 |
|---|---|
| `tools/todo/todo.go` | `emitTodoEvent` 附带 `todoSnapshot` 到 `Event.Data`;新增 `todoSnapshot` 类型 |
| `tools/todo/todo_test.go` | 断言 progress 事件携带结构化 `Data`(items / done / total 正确) |
| `cmd/runcode-desktop/frontend/src/bridge.ts` | `ToolEvent.data?`;新增 `PlanItem` / `PlanSnapshot` 类型 |
| `cmd/runcode-desktop/frontend/src/App.tsx` | `plan` 状态 + TodoWrite 事件解析 + `<PlanPanel>` 组件 + 布局接入 + `groupBlocks` 隐去 TodoWrite 行 |
| `cmd/runcode-desktop/frontend/src/styles.css` | 面板 / 细条 / 滑动动画所需样式(若无法纯用工具类表达) |

`internal/desktop` 与 `pkg/tool` **无需改动**(复用既有 `Event.Data` 与透传管线)。

## 7. 测试策略

- **后端**:`tools/todo` 单测断言 progress 事件的 `Data` 为 `todoSnapshot`,含正确的 items、done、total,并覆盖空表 / 超限 / 多个 in_progress 的既有校验不回归。
- **前端**:以现有前端无单测框架为准,采用手动验证(`wails dev`):
  1. 触发一个多步任务,确认面板滑出、进度条与高亮项随对话实时更新。
  2. 全部完成后确认自动收成细条,点击可重新展开。
  3. 普通单轮问答确认面板保持收起、消息流占满宽度。
  4. 确认消息流中不再出现「规划任务」工具行。

## 8. 实施阶段

1. 后端:`todoSnapshot` + `emitTodoEvent` 改造 + 单测。
2. 前端类型:`bridge.ts` 增补。
3. 前端组件:`<PlanPanel>`(展开/收起两态 + 进度条 + 列表项)。
4. 前端接入:`App.tsx` 状态、事件解析、布局、`groupBlocks` 隐行。
5. 手动验证 + 细节打磨(动画、配色、空态)。
