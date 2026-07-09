# 「已编辑 · 撤销 · 审核」编辑卡片

日期：2026-07-09
状态：已批准设计方向（本轮 Q&A：审核=逐卡内联红绿 diff / 撤销=恢复+确认 / 跨会话 resume 本轮也做 / 每文件一张卡 / 捕获点在 executor、工具不改），待写实现计划

## 背景

产物功能第 6 轮。参考图（`86dc86265b223de2bc3570edfe18b751.jpg`）里，AI 编辑文件后除了现有的中性产物卡，还有一张独立的「已编辑」卡：编辑态图标 + 文件名 + `+665 -0` + `撤销 ↩` + `审核`；点「审核」进入该回合改动的红绿 diff。

现状缺口（探查结论）：

- **编辑前内容被丢弃**。`tools/write`、`tools/edit` 在写入前读了旧内容（`previous` / `text`），算完一个**有损截断**的展示 diff（`internal/diff`：`Context:3 / MaxLines:200 / MaxInput:2000`）后即丢弃，不设 `tool.Event.Data`。
- **`+N -N` 不准**。前端 `diffStats`（`App.tsx`）是数 `diff_add`/`diff_del` 行，executor 侧又二次截断到 100 行（`maxToolEventOutputLines`）。故 `+665` 这类大改今天根本显示不出来（会显示 ~100，>2000 行文件显示 0）。
- **无 git、无快照、无回合级元数据**。工作区不保证是 git 仓；全仓无 `exec.Command("git")` 生产代码；持久化只有 append-only 的 `llm.Message` JSONL（`<workspace>/.runcode/sessions/<id>.jsonl`），resume 只能重建工具名/路径/结果文本，丢 diff 与统计。
- **无写回工作区的绑定**。`*App` 只有只读的 `ReadArtifact`；`SaveProjectContext` 是最接近的写模板但固定路径。

**结论**：撤销、审核、准确 `+N-N` 都卡在同一件事——**捕获编辑前快照**。补上这一个能力即全部解锁。这是本设计的地基。

## 范围

**做**：
1. 捕获编辑前/后内容的地基（executor 级挂钩 + 桌面专属 recorder + `.runcode/edits/` 侧车持久化 + 准确行数统计）。
2. 撤销：`RevertEdit` 绑定（写回旧字节 / 新建文件则删除，工作区边界 fail-closed）。
3. 审核：`ReviewEdit` 绑定（基线 vs 该轮内容的红绿 diff）+ 前端预览面板新增 diff 标签。
4. 「已编辑」卡片（每文件一张，撤销内联确认 / 审核开 diff 标签），挂在每个回合正则产物卡之后。
5. 跨会话：`ListEdits` 绑定 + resume 后按 `turnSeq` 把卡挂回对应回合（撤销/审核因快照落盘而免费可用）。

**不做**（本轮 YAGNI）：
- 独立的「`上轮对话 ▾` 回合选择器 + 多文件汇总审查面板」（用户已选逐卡内联；回合归属由卡挂在对话回合下天然表达）。
- `Delete` 工具的「已删除」卡（语义不同；地基可平滑扩展，留待需要时）。
- 快照的自动清理/配额（本轮全会话保留；见「后续」）。
- 子代理、CLI 的编辑卡（recorder 仅桌面主会话注入，天然不触发）。

跨平台仍是硬约束（Mac/Win/Linux）；所有改动为纯 Go + 前端，天然跨平台；不引入新依赖、不 shell out。

## 架构

三层，低耦合。core 只加一个小接口和一处编排挂钩，桌面实现具体行为，CLI 因 recorder 为 nil 而零影响。

### ① 捕获点：executor 级（core，工具不改）

`internal/repl/executor.go` 在 `e.runTool` 前后加挂钩。executor 已知工具名（`req.Name`）与原始入参（`req.Input`，含 `path`）。

core 侧新增接口（定义在 `internal/repl` 或 `pkg/tool`，由桌面实现）：

```go
// EditRecorder 捕获一次文件变更的前/后内容，返回给 UI 的元数据。
// old 为编辑前字节（existed=false 时忽略）；neu 为编辑后字节。
type EditRecorder interface {
    Record(relPath string, existed bool, old, neu []byte) (EditRecord, error)
}

// EditRecord 是一次编辑的 UI 元数据，随 completed 事件的 Data 下发。
type EditRecord struct {
    SnapshotID string // 撤销/审核句柄（= 本回合该文件的基线快照 id）
    TurnSeq    int    // 用户回合序号（1 起），live 与 resume 对齐用
    RelPath    string // 工作区相对路径（归一为 / 分隔）
    Added      int    // 准确新增行（基线 vs 该轮内容，非截断）
    Removed    int    // 准确删除行
    Created    bool   // 原文件不存在 → 撤销 = 删除
}
```

executor 逻辑（仅当 `e.editRecorder != nil` 且 `req.Name ∈ {Write, Edit}` 且入参可解析出 `path`）：

1. 运行前：把 `path` 相对 `tctx.WorkingDirectory` 解析并校验在工作区内；若存在则读旧字节 `old`、`existed=true`，否则 `existed=false`。解析失败/越界 → 跳过捕获（不影响工具运行）。
2. `result, err := e.runTool(...)`（保持原样）。
3. 运行**成功**（`err==nil && !result.IsError`）后、构造 `completed` 事件时（executor.go:270-275）：读回新字节 `neu`，调 `e.editRecorder.Record(rel, existed, old, neu)`，成功则 `event.Data = editRecord`。
4. `Record` 出错（磁盘满等）→ 吞掉并记一条 warning，**不设 Data、不影响工具结果**。

engine 注入（与 `ExtraTools` 一致，仅主会话）：`engine.Options` 加 `EditRecorder EditRecorder`；`build.go` 把它传给 executor 构造；桌面 `internal/desktop/app.go` 传入 editstore 实例，CLI 传 nil。

> 关键取舍：地基放在**编排层（executor）**而非改 `tools/write`、`tools/edit` 本身——工具保持纯净、单一职责；捕获与工具解耦；CLI 因 recorder 为 nil 完全不受影响。

### ② 快照仓：`internal/desktop/editstore.go`（新）

桌面实现 `EditRecorder`，并持有会话级状态。

- **持久化布局**：`<workspace>/.runcode/edits/<sessionID>/`
  - `base-<id>` —— 基线快照（编辑前旧字节）。
  - `after-<id>` —— 该回合结束时的新内容（审核「之后」侧、撤销安全比对用）。
  - `index.jsonl` —— 每基线一条：`{id, turnSeq, relPath, created, added, removed, reverted}`。
- **回合基线**：`BeginTurn(turnSeq int)`（App 在 `SendMessage`/发消息时调，`turnSeq` 为递增用户回合序号）重置「本回合已快照文件」集合。
- **`Record(rel, existed, old, neu)`**：
  - 本回合**首次**改 `rel` → 写 `base-<id>`（`old`），`created = !existed`，记 `SnapshotID=id`、`TurnSeq=当前回合`。
  - 本回合**再次**改 `rel` → 复用同一 `SnapshotID`（基线不变）。
  - 每次都用 `neu` **覆盖** `after-<id>`（保留该回合最新内容）；用 `diff.Stat(base, neu)` 算准确 `Added/Removed`；更新 index 该条。
  - 返回 `EditRecord`。
- **`Revert(id)`**：读 index 条目；`created` → 删除当前文件（工作区边界校验；用 `toolpath.ResolveMutationTarget` 模式定位）；否则把 `base-<id>` 字节写回 `relPath`（边界校验；重建缺失父目录同 Write）。写回后标 `reverted=true`。当前文件字节与 `after-<id>` 不一致 → 仍执行但由前端确认框已提示（见错误处理）。
- **`Diff(id)`**：读 `base-<id>` 与 `after-<id>`，`diff.Unified` 出结构化红绿行（宽松上限，见错误处理），返回 `EditDiff{RelPath, Created, Lines}`。
- **`List()`**：读 `index.jsonl` 全部条目，映射为 `[]EditRecord`（供 resume 渲染）。

准确统计：`internal/diff` 新增 `Stat(old, neu string) (added, removed int)`——复用现有 diff 算法但**不做展示截断**（仍对超大输入设一个远高于 200 的护栏并如实标注）。

### ③ 绑定：`*App` 新增方法（Wails `Bind: []any{app}` 自动暴露）

```go
func (a *App) RevertEdit(snapshotID string) error
func (a *App) ReviewEdit(snapshotID string) (EditDiff, error)
func (a *App) ListEdits() ([]EditRecord, error)
```

- 三者都在 `a.mu` 下取 `a.workspace` 与当前 editstore，越权/越界 fail-closed。
- `RevertEdit` 是唯一的「写回工作区」绑定，边界校验复用 `resolveWithinWorkspace`（覆盖已存在文件）/ `ResolveMutationTarget`（重建缺失路径）。

## 数据流

```
用户发消息 → App 发送路径 → editStore.BeginTurn(turnSeq++)（重置本回合基线集）
模型调 Write/Edit → executor 运行前读旧字节、成功后读新字节
  → editStore.Record(rel, existed, old, neu)
       首次改该文件 → 写 base-<id>；再次 → 复用 id；每次覆盖 after-<id> + 更新 index + diff.Stat 算准确 ±
  → EditRecord 挂到 completed 事件 Data
桌面 pumpToolEvents 原样转发 tool:event → 前端读 event.data
前端按 (turnSeq, relPath) 归并 → 每文件一张 EditedCard，贴在该回合 ReplyArtifacts 之后
  · 撤销 → 卡内联「确认撤销?」→ RevertEdit(id) → 标记已撤销、刷新 files
  · 审核 → ReviewEdit(id) → 预览面板开 {kind:'diff'} 标签，红绿渲染
resume 旧会话 → 前端 ListEdits() → 按 turnSeq 把卡挂回对应回合（撤销/审核因快照落盘照常可用）
CLI：editRecorder = nil → Data 不设 → 无卡、无绑定、行为不变
```

`turnSeq` 对齐：`turnSeq` = 会话内用户回合序号（1 起）。live 由 editstore 在 `BeginTurn` 递增并随 `EditRecord` 下发；resume 时前端按重建对话里用户消息的序号计算，两侧一致，从而把持久化的 edit 记录挂回正确回合。

## 前端组件

- **`edited-card.tsx`（新）**：`EditedCard({rec, onUndo, onReview, reverted})`——编辑态图标 + 文件名 + `+Added −Removed` + `撤销` + `审核`。撤销走**卡内联确认**（按钮就地变「确认撤销? 是 / 否」），不弹原生对话框（跨平台、风格统一）；撤销后卡片转灰显示「已撤销」。
- **预览面板 diff 标签**：`preview-tabs.ts` 的 tab 从 `{relPath}` 扩为可判别联合，新增 `{kind:'diff', snapshotID, relPath}`；`preview-panel.tsx` 对 diff tab 调 `reviewEdit` 拿 `EditDiff`，复用现有 `lineClass` 红绿样式逐行渲染。
- **`bridge.ts`**：`revertEdit(id)`、`reviewEdit(id)`、`listEdits()` 包装；`EditRecord`/`EditDiff` 类型；`ToolEvent.data` 收窄为 `EditRecord | PlanSnapshot | undefined`。
- **`App.tsx`**：
  - live 捕获：Write/Edit `completed` 事件带 `data` 为 EditRecord 时，按 `(turnSeq, relPath)` 归并进 chat 模型（同回合同文件复用同 SnapshotID → 一张卡，取最新统计）。
  - 渲染落点：assistant 回合 `ReplyArtifacts`（正则卡）之后，渲染该回合的 EditedCards。
  - resume 渲染：会话载入后调 `listEdits()`，按 `turnSeq` 分组，在对应重建回合下渲染 EditedCards。

## 错误处理 / 安全

- **捕获失败不波及编辑**：`Record` 出错只是不出卡（executor 吞错 + warning），工具结果原样返回。
- **写回边界 fail-closed**：`RevertEdit` 越界一律拒绝；`created` 时删除也走同一边界校验；重建缺失路径用 `ResolveMutationTarget` 模式（与 Write 一致）。
- **撤销安全 / 覆盖提示**：撤销前前端已弹内联确认；当前文件字节 ≠ `after-<id>`（AI 改动后又被模型或用户改过）时，确认文案额外提示「之后又被改动过，撤销将覆盖」。
- **撤销幂等**：已 `reverted` 或快照缺失 → 绑定返回明确错误，前端提示，不崩。
- **大文件护栏**：快照读写留意 Write 的量级；`diff.Stat` 与 `ReviewEdit` 的 diff 设远高于 200 的上限，超限如实标注「差异过大，已截断」。快照本身按需落盘（不常驻内存）。
- **仅桌面 / 仅主会话**：`EditRecorder` 与 `ExtraTools`/`open_preview` 一致，仅桌面主会话注入；子代理快照之后不追加，CLI 传 nil。
- **不改工具语义**：Write/Edit/Delete 的既有行为、gate、结果文本一律不变。

## 测试计划

**Go**：
- `diff.Stat`：新增/删除/改的准确行数；空↔非空（新建/清空）；超大输入的护栏与标注。
- `editStore`：`Record` 首次写基线、二次复用同 `SnapshotID`、`created` 判定；`after` 每次被覆盖；`Stat` 写入 index；`BeginTurn` 重置回合集使新回合另起基线。
- `RevertEdit`：非 `created` 写回 = 原旧字节；`created` 删除文件；越界拒绝；快照缺失/已撤销报错。
- `ReviewEdit`：基线 vs after 的红绿行；`Created` 标志。
- `ListEdits`：读回全部条目、`turnSeq` 正确。
- 引擎：`opts.EditRecorder` 被接到 executor（小测试或借现有 build 测试覆盖）。

**前端（vitest）**：
- event.data(EditRecord) → 按 `(turnSeq, relPath)` 归并：同回合同文件一张卡、取最新统计；不同回合分卡。
- 撤销内联确认状态机（点撤销→确认/取消→已撤销）。
- diff tab 可判别联合的开合与去重（`preview-tabs`）。
- resume：`listEdits()` 结果按 `turnSeq` 分组挂回回合。

**手动（桌面）**：
- 让模型改一个 md → 出「已编辑」卡带**准确** `+N-N` → 审核开红绿 diff → 撤销确认后文件复原、卡片转「已撤销」。
- 新建文件 → 撤销 = 删除该文件。
- 同一文件一回合内改两次 → 一张卡、统计为累计。
- 关闭重开该会话（resume）→ 卡按回合复现，审核/撤销仍可用。
- CLI 跑 Write/Edit → 无编辑卡、行为不变。

## 落点（文件）

- core：`internal/repl/executor.go`（挂钩 + `event.Data`）、`internal/repl` 或 `pkg/tool`（`EditRecorder`/`EditRecord`/`EditDiff` 类型）、`internal/engine/engine.go`（`Options.EditRecorder`）、`internal/engine/build.go`（传给 executor）、`internal/diff/diff.go`（`Stat`）。
- 桌面：`internal/desktop/editstore.go`（新，实现 recorder + 侧车 + Revert/Diff/List）、`internal/desktop/app.go`（`BeginTurn` 接入发送路径、注入 recorder）、绑定方法（editstore.go 或 preview.go 旁）、`internal/desktop/workspacepath.go`（复用边界校验）。
- 前端：`edited-card.tsx`（新）、`preview-tabs.ts`（tab 联合）、`preview-panel.tsx`（diff 渲染）、`bridge.ts`（绑定包装 + 类型）、`App.tsx`（live 归并 + 落点渲染 + resume 渲染）、`chat.ts`（模型里存回合 edits）。

## 已定决策

- 捕获点：**executor 级、Write/Edit 工具不改**；recorder 仅桌面主会话注入（`engine.Options.EditRecorder`），CLI 为 nil 零影响。
- 审核：**逐卡内联**红绿 diff 标签（预览面板）；= `基线 vs 该轮结束内容`，不被后续回合污染；**不做**独立回合选择器汇总面板。
- 撤销：**恢复 + 卡内联确认**；`created` → 删除；越界 fail-closed；覆盖后续改动时确认文案提示。
- 卡片：**每文件一张**，挂在回合正则产物卡之后。
- 跨会话：快照持久化 `.runcode/edits/<sessionID>/`；**本轮**含 resume 后按 `turnSeq` 重渲染卡 + 绑定（`ListEdits`）。
- 范围：仅 `Write`/`Edit`；`Delete` 与快照清理留作后续。
