# 「已编辑 · 撤销 · 审核」编辑卡片 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给桌面版加「已编辑」卡片——AI 编辑文件后，在对话回合下方为每个被改文件出一张卡（准确 `+N -N`），点「撤销」恢复到该回合改动前（新建则删除），点「审核」在预览面板开一个红绿 diff 标签；resume 旧会话后卡片按 tool-use id 复现、撤销/审核仍可用。

**Architecture:** 地基是「捕获编辑前/后内容」。在 **executor**（`internal/repl`）层为 Write/Edit 前后加挂钩：核心只做括弧，所有文件 IO 与快照落在桌面实现的 `EditRecorder` 里（CLI 传 nil，零影响）。桌面 `editStore` 把每次编辑的基线快照（`base-<id>`）与该回合最新内容（`after-<id>`）落到 `<workspace>/.runcode/edits/<sessionID>/`，并把 `EditRecord`（snapshotId / toolUseId / relPath / added / removed / created）作为该工具 `completed` 事件的 `Data` 下发。前端把带 `data` 的 Write/Edit 工具块在 `groupBlocks` 里归并成一个 `edits` 渲染组（每文件一张卡）；撤销/审核走新增的 `RevertEdit`/`ReviewEdit` 绑定。resume 时 `ListEdits` 按 **tool-use id** 把 `EditRecord` 贴回重建的工具块，走完全相同的前端路径。

> **相对 spec 的一处细化（更稳健）**：spec 用 `turnSeq`（用户回合序号）对齐 live/resume；实现改用 **tool-use id** 锚定——`ResumedTool` 天然带 `ToolUseID`，resume 直接把 `EditRecord` 贴到对应工具块，live 与 resume 于是走同一条 `groupBlocks` 路径，避免了跨 resume 的回合计数对齐问题。行为不变，只更稳。`turnSeq` 不再需要。

**Tech Stack:** Go 1.26（标准库；`internal/diff` 的 LCS）、React + TypeScript（vite/esbuild，无 tsc gate）、vitest。Wails v2（`Bind: []any{app}` 自动暴露 `*App` 导出方法）。

## Global Constraints

- 跨平台：Mac / Windows / Linux 都要能跑；无 OS 专属分支（本功能纯 Go 标准库 + 前端，天然跨平台）。
- 不 shell out、不引入新 Go/npm 依赖。
- 工作区边界 **fail-closed**：任何写回/删除/读取都必须先过工作区包含性校验；越界一律拒绝。复用 `internal/desktop/workspacepath.go` 的 `resolveWithinWorkspace`（要求路径已存在）；对可能不存在的写回目标用本计划新增的 `resolveForWrite`（校验最近存在祖先在工作区内）。
- **仅桌面、仅主会话**：`EditRecorder` 与 `open_preview`/`ExtraTools` 一致，仅桌面注入；CLI（`cmd/runcode`）与子代理不传、不触发。
- 不改 Write/Edit/Delete 的既有语义、gate、结果文本；捕获失败绝不让工具失败。
- 快照/审核有大小护栏：单文件快照 `maxEditSnapshotBytes = 4<<20`（4 MiB）以上跳过（不出卡）；审核 diff 用有界 `diff.Options`，超限落到既有「large file diff omitted」info 行。
- Go 提交信息结尾附：`Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`。
- `EditRecord`/`EditDiff` 的 JSON tag 必须是 camelCase（`snapshotId`/`toolUseId`/`relPath`/`added`/`removed`/`created`/`reverted`；`EditDiff`：`relPath`/`created`/`lines`），与前端手写类型一致。

---

### Task 1: 准确行数统计 `diff.Stat`（core）

**Files:**
- Modify: `internal/diff/diff.go`
- Test: `internal/diff/stat_test.go` (create)

**Interfaces:**
- Consumes: 现有 `splitLines`、`lcsOps`（同文件私有函数）。
- Produces: `func Stat(oldText, newText string) (added, removed int)` —— 不做展示截断的准确增删行数；超过 `statMaxInput` 行则退化为粗略估计（把两侧都当作全变更）。

- [ ] **Step 1: 写失败测试**

创建 `internal/diff/stat_test.go`：

```go
package diff

import "testing"

func TestStatCountsAddedAndRemoved(t *testing.T) {
	cases := []struct {
		name             string
		old, new         string
		wantAdd, wantDel int
	}{
		{"new file all additions", "", "a\nb\nc\n", 3, 0},
		{"cleared file all deletions", "a\nb\n", "", 0, 2},
		{"replace one line", "a\nb\nc\n", "a\nB\nc\n", 1, 1},
		{"append lines", "a\n", "a\nb\nc\n", 2, 0},
		{"no change", "a\nb\n", "a\nb\n", 0, 0},
		{"trailing newline irrelevant", "a\nb", "a\nb\n", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			add, del := Stat(c.old, c.new)
			if add != c.wantAdd || del != c.wantDel {
				t.Fatalf("Stat(%q,%q) = (+%d -%d), want (+%d -%d)", c.old, c.new, add, del, c.wantAdd, c.wantDel)
			}
		})
	}
}

func TestStatCountsBeyondDisplayCap(t *testing.T) {
	// 500 added lines — well past Unified's 200-line display cap. Stat must be exact.
	old := ""
	new := ""
	for i := 0; i < 500; i++ {
		new += "line\n"
	}
	add, del := Stat(old, new)
	if add != 500 || del != 0 {
		t.Fatalf("Stat = (+%d -%d), want (+500 -0)", add, del)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/diff/ -run TestStat -v`
Expected: FAIL —— `undefined: Stat`。

- [ ] **Step 3: 实现 `Stat`**

在 `internal/diff/diff.go` 末尾追加：

```go
// statMaxInput bounds the O(n*m) LCS for Stat; past it Stat returns a coarse
// estimate (both sides treated as fully changed) instead of blowing up.
const statMaxInput = 50000

// Stat returns the exact number of added and removed lines between oldText and
// newText, without the display truncation Unified applies. Binary or oversized
// input yields a coarse estimate rather than an exact count.
func Stat(oldText, newText string) (added, removed int) {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	if looksBinary(oldText) || looksBinary(newText) {
		return len(newLines), len(oldLines)
	}
	if len(oldLines) > statMaxInput || len(newLines) > statMaxInput {
		return len(newLines), len(oldLines)
	}
	for _, o := range lcsOps(oldLines, newLines) {
		switch o.kind {
		case opInsert:
			added++
		case opDelete:
			removed++
		}
	}
	return added, removed
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/diff/ -v`
Expected: PASS（新测试 + 既有测试全绿）。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/diff/ && go build ./...
git add internal/diff/diff.go internal/diff/stat_test.go
git commit -m "diff: add Stat for exact added/removed line counts" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: `EditRecorder` 接口 + executor 捕获挂钩（core）

**Files:**
- Create: `internal/repl/editrecorder.go`
- Modify: `internal/repl/executor.go`
- Test: `internal/repl/edit_capture_test.go` (create)

**Interfaces:**
- Produces（`internal/repl`）：
  ```go
  type EditRecorder interface { BeginEdit(relPath, toolUseID string) EditHandle }
  type EditHandle   interface { Commit() (data any, err error) }
  ```
- Consumes: `toolpath.WorkspaceRoot(tctx)`、`toolpath.Resolve(path, tctx)`（现有）。
- `Executor` 新增字段 `editRecorder EditRecorder`；`ExecutorOptions` 新增同名字段；`NewExecutorWithOptions` 透传。executor 在 Write/Edit 成功后把 `EditHandle.Commit()` 返回的 `data` 挂到 `completed` 事件的 `event.Data`。

- [ ] **Step 1: 写失败测试**

创建 `internal/repl/edit_capture_test.go`。用一个假 recorder 断言 executor 在 Write 成功时调用 `BeginEdit`→`Commit` 并把返回值放进 `completed` 事件的 `Data`；失败工具不调用 `Commit`。

> 复用本包 `executor_test.go` 里已有的辅助（同 `repl` 包，直接可用）：`allowAllPolicy{}`（放行策略，绕过 safe 模式对 Write 的默认拒绝——见 `TestExecutorDeniesWriteInDefaultSafeModeWithoutRunning`）、`rawInput(t, map)`、`drainToolEvents(events)`。

```go
package repl

import (
	"context"
	"testing"

	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools"
)

type fakeHandle struct {
	committed *bool
	payload   any
}

func (h fakeHandle) Commit() (any, error) { *h.committed = true; return h.payload, nil }

type fakeRecorder struct {
	beganRel string
	beganTUI string
	handle   EditHandle
}

func (r *fakeRecorder) BeginEdit(relPath, toolUseID string) EditHandle {
	r.beganRel = relPath
	r.beganTUI = toolUseID
	return r.handle
}

func TestExecutorAttachesEditData(t *testing.T) {
	ws := t.TempDir()
	committed := false
	rec := &fakeRecorder{handle: fakeHandle{committed: &committed, payload: map[string]any{"snapshotId": "s1"}}}
	exec, err := NewExecutorWithOptions(ExecutorOptions{
		Tools:        tools.Builtins(),
		Permissions:  permissions.NewService(permissions.Options{Policy: allowAllPolicy{}}),
		EditRecorder: rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan tool.Event, 16)
	// A brand-new file avoids the read-before-write gate, so allow-all lets Write run.
	tctx := &tool.Context{WorkingDirectory: ws, ReadSet: map[string]tool.ReadFile{}}
	_, err = exec.Execute(context.Background(), ExecuteRequest{
		Name:      "Write",
		Input:     rawInput(t, map[string]any{"path": "note.md", "content": "hello\n"}),
		ToolUseID: "tu-1",
		Context:   tctx,
		Events:    events,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !committed {
		t.Fatal("Commit was not called on a successful Write")
	}
	if rec.beganRel != "note.md" || rec.beganTUI != "tu-1" {
		t.Fatalf("BeginEdit got (%q,%q), want (note.md, tu-1)", rec.beganRel, rec.beganTUI)
	}
	// Drain events; the completed event must carry Data.
	var gotData any
	for _, ev := range drainToolEvents(events) {
		if ev.Type == tool.EventTypeCompleted {
			gotData = ev.Data
		}
	}
	if gotData == nil {
		t.Fatal("completed event has no Data")
	}
}
```

> 实现者注意：`tools.Builtins()`（`tools/registry.go:22`）含真实 `Write` 工具；`executor_test.go` 已用同样方式。目标是用真实 Write 工具真正写文件、走成功分支，测试聚焦在「Data 被挂上」。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/repl/ -run TestExecutorAttachesEditData -v`
Expected: FAIL —— `ExecutorOptions` 无 `EditRecorder` 字段（编译错误），或 Data 为 nil。

- [ ] **Step 3: 新增接口文件**

创建 `internal/repl/editrecorder.go`：

```go
package repl

// EditRecorder captures the pre/post content of a Write/Edit mutation so a host
// (the desktop) can offer undo/review. The core does no file IO itself: the
// executor only brackets the tool call and hands the recorder the mutation's
// workspace-relative path and tool-use id. CLI leaves it nil (no capture).
type EditRecorder interface {
	// BeginEdit is called just before a Write/Edit runs. It returns a handle whose
	// Commit is called iff the tool succeeds, or nil to skip recording this edit.
	BeginEdit(relPath, toolUseID string) EditHandle
}

// EditHandle finishes one capture. Commit reads the post-edit state and returns the
// opaque payload to attach to the tool event's Data (nil to attach nothing). The
// core treats the payload as opaque; the desktop defines its shape (EditRecord).
type EditHandle interface {
	Commit() (data any, err error)
}

// isEditTool reports whether name is a file-mutating tool this layer snapshots.
func isEditTool(name string) bool { return name == "Write" || name == "Edit" }

// editMutationRelPath parses the workspace-relative target of a Write/Edit from its
// raw input, resolved against the workspace root. It returns ("", false) when the
// input has no usable path or the target escapes the workspace.
func editMutationRelPath(input []byte, tctx *tool.Context) (string, bool) {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.Path == "" {
		return "", false
	}
	root, err := toolpath.WorkspaceRoot(tctx)
	if err != nil {
		return "", false
	}
	abs, err := toolpath.Resolve(in.Path, tctx)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}
```

在文件顶部补 import：

```go
import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
)
```

- [ ] **Step 4: 接线 executor**

在 `internal/repl/executor.go`：

(a) `Executor` 结构体（line 41-45）加字段：

```go
type Executor struct {
	tools        map[string]tool.Tool
	permissions  *permissions.Service
	hooks        hooks.Runner
	editRecorder EditRecorder
}
```

(b) `ExecutorOptions`（line 47-53）加字段：

```go
	// EditRecorder, when set, captures Write/Edit pre/post content for host-side
	// undo/review. nil (the default, e.g. CLI) disables capture.
	EditRecorder EditRecorder
```

(c) `NewExecutorWithOptions` 的 return（line 106）带上：

```go
	return &Executor{tools: indexed, permissions: permissionService, hooks: hookRunner, editRecorder: opts.EditRecorder}, nil
```

(d) 在 `Execute` 里，`readSetBefore := ...`（line 213）与 `result, err := e.runTool(...)`（line 214）之间，插入捕获开始：

```go
	var editHandle EditHandle
	if e.editRecorder != nil && isEditTool(req.Name) {
		if rel, ok := editMutationRelPath(req.Input, tctx); ok {
			editHandle = e.editRecorder.BeginEdit(rel, tctx.ToolUseID)
		}
	}
	readSetBefore := cloneReadSetForEvents(tctx.ReadSet)
	result, err := e.runTool(ctx, runner, req, tctx)
```

(e) 在成功分支（line 269-276 的 `else` 块）里，构造 `completed` 事件后、`emitToolEvent` 前，挂上 Data：

```go
	} else {
		event := executorToolEvent(tool.EventTypeCompleted, req.Name, tctx.ToolUseID, "completed")
		event.Files = readFiles
		event.FilesTotal = readFilesTotal
		attachToolOutput(&event, outputLines, outputTotal, outputTruncated)
		event.Image = imageForEvent(result)
		if editHandle != nil {
			if data, cerr := editHandle.Commit(); cerr == nil && data != nil {
				event.Data = data
			}
		}
		emitToolEvent(req.Events, event)
	}
```

> 注意：只有成功（`else` 分支）才 Commit。失败（line 217 的 `err != nil`）与 `result.IsError`（line 263）分支不 Commit——文件可能没变。`editHandle` 在这些分支被丢弃（无副作用，`BeginEdit` 未写任何东西）。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/repl/ -run 'TestExecutor' -v`
Expected: PASS —— 新捕获测试通过，既有 executor 测试不回归。

- [ ] **Step 6: 提交**

```bash
gofmt -w internal/repl/ && go build ./...
git add internal/repl/editrecorder.go internal/repl/executor.go internal/repl/edit_capture_test.go
git commit -m "repl: capture Write/Edit pre/post content via optional EditRecorder" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: 透传 EditRecorder（session + engine）

**Files:**
- Modify: `internal/repl/session.go`
- Modify: `internal/engine/engine.go`
- Modify: `internal/engine/build.go`

**Interfaces:**
- Consumes: `repl.EditRecorder`（Task 2）。
- Produces: `repl.SessionOptions.EditRecorder`、`engine.Options.EditRecorder`（均为 `repl.EditRecorder`），Build 把它接到 executor。

- [ ] **Step 1: `SessionOptions` 加字段并接线 executor**

在 `internal/repl/session.go` 的 `SessionOptions`（line 51-101），在 `Thinking` 后加：

```go
	// EditRecorder, when set, captures Write/Edit pre/post content for host-side
	// undo/review (desktop only). nil disables it.
	EditRecorder EditRecorder
```

在 `NewSession` 里（line 225）把它带进 executor：

```go
	executor, err := NewExecutorWithOptions(ExecutorOptions{Tools: tools, Permissions: opts.Permissions, Hooks: hookRunner, EditRecorder: opts.EditRecorder})
```

- [ ] **Step 2: `engine.Options` 加字段**

在 `internal/engine/engine.go` 的 `Options`（line 26-58），在 `ExtraTools` 后加：

```go
	// EditRecorder, when set, is threaded to the session's executor so the host can
	// capture Write/Edit pre/post content for undo/review. The desktop supplies its
	// editStore; the CLI leaves it nil.
	EditRecorder repl.EditRecorder
```

engine.go 已 import `internal/repl`（line 12）。

- [ ] **Step 3: `build.go` 透传到 `repl.NewSession`**

在 `internal/engine/build.go` 的 `repl.NewSession(repl.SessionOptions{...})`（line 195-222），在 `Thinking: cfg.Thinking,` 后加一行：

```go
		Thinking:      cfg.Thinking,
		Reasoning:     reasoningOptions(cfg.ReasoningScenario),
		EditRecorder:  opts.EditRecorder,
```

（保持 gofmt 对齐即可。）

- [ ] **Step 4: 编译**

Run: `go build ./... && go test ./internal/repl/ ./internal/engine/ -run 'TestExecutor|TestBuild' -count=1`
Expected: 编译通过；相关测试绿（本步是纯接线，无新行为，靠 Task 2 测试与既有 build 测试覆盖）。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/repl/ internal/engine/ && go build ./...
git add internal/repl/session.go internal/engine/engine.go internal/engine/build.go
git commit -m "engine: thread EditRecorder to the session executor" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: 桌面快照仓 `editStore`（含边界写回助手）

**Files:**
- Create: `internal/desktop/editstore.go`
- Test: `internal/desktop/editstore_test.go` (create)

**Interfaces:**
- Consumes: `internal/diff`（`Stat`、`Unified`）、`internal/repl`（`EditRecorder`/`EditHandle`）、`pkg/tool`（`OutputLine`）、`workspacepath.go` 的 `resolveWithinWorkspace`。
- Produces:
  ```go
  type EditRecord struct { SnapshotID, ToolUseID, RelPath string; Added, Removed int; Created, Reverted bool }  // json camelCase
  type EditDiff  struct { RelPath string; Created bool; Lines []tool.OutputLine }                                // json camelCase
  func newEditStore() *editStore
  func (s *editStore) BeginSession(ws, sessionID string)   // 绑定目录、载入 index、重置回合基线
  func (s *editStore) BeginTurn()                           // 清空本回合基线映射
  func (s *editStore) BeginEdit(relPath, toolUseID string) repl.EditHandle  // 实现 repl.EditRecorder
  func (s *editStore) Revert(snapshotID string) error
  func (s *editStore) Diff(snapshotID string) (EditDiff, error)
  func (s *editStore) List() []EditRecord
  ```
- 每回合首次改某文件 → 写 `base-<id>`（该回合基线）；后续改同文件复用同 `id`。每次改都覆盖 `after-<id>` 为最新内容并用 `diff.Stat(base, after)` 更新 `Added/Removed`。`index.jsonl` 每次编辑一行（同一 `id` 可多行，`ToolUseID` 不同）。

- [ ] **Step 1: 写失败测试**

创建 `internal/desktop/editstore_test.go`：

```go
package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// simulateEdit runs the BeginEdit(pre-state)→mutate→Commit(post-state) bracket the
// executor performs, and returns the recorded EditRecord.
func simulateEdit(t *testing.T, s *editStore, ws, rel, toolUseID, newContent string) EditRecord {
	t.Helper()
	h := s.BeginEdit(rel, toolUseID)
	if h == nil {
		t.Fatalf("BeginEdit(%q) returned nil handle", rel)
	}
	writeFile(t, filepath.Join(ws, rel), newContent) // the "tool" writes
	data, err := h.Commit()
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := data.(EditRecord)
	if !ok {
		t.Fatalf("Commit payload is %T, want EditRecord", data)
	}
	return rec
}

func TestEditStoreRecordCreateThenRevertDeletes(t *testing.T) {
	ws := t.TempDir()
	s := newEditStore()
	s.BeginSession(ws, "sess1")
	s.BeginTurn()

	rec := simulateEdit(t, s, ws, "out/new.md", "tu1", "line1\nline2\nline3\n")
	if !rec.Created || rec.Added != 3 || rec.Removed != 0 {
		t.Fatalf("record = %+v, want Created +3 -0", rec)
	}
	// Undo of a newly created file deletes it.
	if err := s.Revert(rec.SnapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, "out/new.md")); !os.IsNotExist(err) {
		t.Fatalf("expected file deleted after revert, stat err = %v", err)
	}
}

func TestEditStoreRevertRestoresBaseline(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.md"), "original\n")
	s := newEditStore()
	s.BeginSession(ws, "sess1")
	s.BeginTurn()

	rec := simulateEdit(t, s, ws, "a.md", "tu1", "changed\n")
	if rec.Created {
		t.Fatal("existing file must not be Created")
	}
	if err := s.Revert(rec.SnapshotID); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(ws, "a.md"))
	if string(got) != "original\n" {
		t.Fatalf("after revert content = %q, want original", got)
	}
}

func TestEditStoreSecondEditReusesBaselineAndAccumulates(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.md"), "l1\nl2\n")
	s := newEditStore()
	s.BeginSession(ws, "sess1")
	s.BeginTurn()

	r1 := simulateEdit(t, s, ws, "a.md", "tu1", "l1\nl2\nl3\n")     // +1
	r2 := simulateEdit(t, s, ws, "a.md", "tu2", "l1\nl2\nl3\nl4\n") // +2 vs baseline
	if r1.SnapshotID != r2.SnapshotID {
		t.Fatalf("second edit must reuse baseline id: %q vs %q", r1.SnapshotID, r2.SnapshotID)
	}
	if r2.Added != 2 || r2.Removed != 0 {
		t.Fatalf("cumulative stat = +%d -%d, want +2 -0", r2.Added, r2.Removed)
	}
	// Revert takes the file back to the turn's baseline, not just the last edit.
	if err := s.Revert(r2.SnapshotID); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(ws, "a.md"))
	if string(got) != "l1\nl2\n" {
		t.Fatalf("after revert = %q, want baseline l1\\nl2\\n", got)
	}
}

func TestEditStoreDiffAndListSurviveReopen(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.md"), "old\n")
	s := newEditStore()
	s.BeginSession(ws, "sess1")
	s.BeginTurn()
	rec := simulateEdit(t, s, ws, "a.md", "tu1", "new\n")

	// Reopen: a fresh store bound to the same session must recover the record + diff.
	s2 := newEditStore()
	s2.BeginSession(ws, "sess1")
	list := s2.List()
	if len(list) != 1 || list[0].ToolUseID != "tu1" || list[0].RelPath != "a.md" {
		t.Fatalf("List after reopen = %+v", list)
	}
	d, err := s2.Diff(rec.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if d.RelPath != "a.md" || len(d.Lines) == 0 {
		t.Fatalf("Diff = %+v", d)
	}
}

func TestEditStoreBeginTurnStartsNewBaseline(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.md"), "v0\n")
	s := newEditStore()
	s.BeginSession(ws, "sess1")

	s.BeginTurn()
	r1 := simulateEdit(t, s, ws, "a.md", "tu1", "v1\n")
	s.BeginTurn() // new turn: next edit re-baselines from current (v1)
	r2 := simulateEdit(t, s, ws, "a.md", "tu2", "v2\n")
	if r1.SnapshotID == r2.SnapshotID {
		t.Fatal("a new turn must create a fresh baseline for the same file")
	}
	// Reverting the second turn goes back to v1 (that turn's baseline), not v0.
	if err := s.Revert(r2.SnapshotID); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(ws, "a.md"))
	if string(got) != "v1\n" {
		t.Fatalf("after revert = %q, want v1", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop/ -run TestEditStore -v`
Expected: FAIL —— `newEditStore`/`editStore` 未定义（编译错误）。

- [ ] **Step 3: 实现 `editStore`**

创建 `internal/desktop/editstore.go`：

```go
package desktop

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/wt68/runcode/internal/diff"
	"github.com/wt68/runcode/internal/repl"
	"github.com/wt68/runcode/pkg/tool"
)

// maxEditSnapshotBytes bounds a single pre/post snapshot. Larger files are skipped
// (no edit card) so undo/review never hold huge blobs in memory or on disk.
const maxEditSnapshotBytes = 4 << 20

// reviewDiffOptions bounds the review diff. It is far more generous than the tool
// step's DefaultOptions so a real edit (hundreds of lines) renders in full; past
// MaxInput it falls back to the "large file diff omitted" info line.
var reviewDiffOptions = diff.Options{Context: 3, MaxLines: 4000, MaxInput: 20000}

// EditRecord is the per-edit metadata attached to a Write/Edit tool event's Data
// (live) and returned by ListEdits (resume). Keyed to the frontend by ToolUseID.
type EditRecord struct {
	SnapshotID string `json:"snapshotId"`
	ToolUseID  string `json:"toolUseId"`
	RelPath    string `json:"relPath"`
	Added      int    `json:"added"`
	Removed    int    `json:"removed"`
	Created    bool   `json:"created"`
	Reverted   bool   `json:"reverted,omitempty"`
}

// EditDiff is the red/green review of one edit: the turn baseline vs the turn's
// latest content for that file.
type EditDiff struct {
	RelPath string            `json:"relPath"`
	Created bool              `json:"created"`
	Lines   []tool.OutputLine `json:"lines"`
}

// baselineMeta is the per-snapshot metadata (one baseline = one turn's first edit
// of a file), recovered from the index on reopen.
type baselineMeta struct {
	relPath  string
	created  bool
	reverted bool
}

// editStore captures Write/Edit pre/post content into <ws>/.runcode/edits/<sess>/
// and serves undo/review. One instance per App, rebound per session via
// BeginSession. All state is guarded by mu.
type editStore struct {
	mu       sync.Mutex
	ws       string            // workspace root; "" until BeginSession
	dir      string            // <ws>/.runcode/edits/<sessionID>; "" until BeginSession
	nextID   int               // next baseline id
	baseline map[string]string // relPath -> snapshotID, current turn only
	meta     map[string]baselineMeta
	records  []EditRecord // append-only, one per edit (per tool-use)
}

func newEditStore() *editStore {
	return &editStore{baseline: map[string]string{}, meta: map[string]baselineMeta{}}
}

// BeginSession binds the store to a session's edit directory and loads any existing
// index so undo/review survive reopen. It resets the per-turn baseline map.
func (s *editStore) BeginSession(ws, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.baseline = map[string]string{}
	s.meta = map[string]baselineMeta{}
	s.records = nil
	s.nextID = 1
	if ws == "" || sessionID == "" {
		s.ws = ""
		s.dir = ""
		return
	}
	s.ws = ws
	s.dir = filepath.Join(ws, ".runcode", "edits", sessionID)
	s.loadIndexLocked()
}

// BeginTurn clears the per-turn baseline map so the next edit to a file captures a
// fresh baseline (this turn's undo/review is relative to the turn's start).
func (s *editStore) BeginTurn() {
	s.mu.Lock()
	s.baseline = map[string]string{}
	s.mu.Unlock()
}

// BeginEdit implements repl.EditRecorder. It reads the pre-edit content now (before
// the tool overwrites) and returns a handle to finish on success. Returns nil to
// skip (no dir, oversized, or unreadable).
func (s *editStore) BeginEdit(relPath, toolUseID string) repl.EditHandle {
	s.mu.Lock()
	dir, ws := s.dir, s.workspaceLocked()
	s.mu.Unlock()
	if dir == "" || ws == "" {
		return nil
	}
	abs, err := resolveForWrite(ws, relPath)
	if err != nil {
		return nil
	}
	old, existed, ok := readCapped(abs)
	if !ok {
		return nil // oversized or unreadable → skip
	}
	return &editHandle{store: s, rel: filepath.ToSlash(relPath), abs: abs, toolUseID: toolUseID, old: old, existed: existed}
}

// workspaceLocked returns the bound workspace root. Caller holds mu.
func (s *editStore) workspaceLocked() string { return s.ws }

type editHandle struct {
	store     *editStore
	rel       string
	abs       string
	toolUseID string
	old       []byte
	existed   bool
}

// Commit reads the post-edit content, writes/updates the baseline + after snapshots,
// computes the cumulative stat, appends an index record, and returns the EditRecord.
func (h *editHandle) Commit() (any, error) {
	neu, _, ok := readCapped(h.abs)
	if !ok {
		return nil, nil // post-edit unreadable/oversized → attach nothing
	}
	s := h.store
	s.mu.Lock()
	defer s.mu.Unlock()

	id, isNew := s.baseline[h.rel], false
	if id == "" {
		id = strconv.Itoa(s.nextID)
		s.nextID++
		s.baseline[h.rel] = id
		isNew = true
	}
	if isNew {
		if err := s.writeSnapshotLocked("base-"+id, h.old); err != nil {
			return nil, err
		}
		s.meta[id] = baselineMeta{relPath: h.rel, created: !h.existed}
	}
	if err := s.writeSnapshotLocked("after-"+id, neu); err != nil {
		return nil, err
	}
	baseBytes, _ := s.readSnapshotLocked("base-" + id)
	added, removed := diff.Stat(string(baseBytes), string(neu))
	rec := EditRecord{
		SnapshotID: id,
		ToolUseID:  h.toolUseID,
		RelPath:    h.rel,
		Added:      added,
		Removed:    removed,
		Created:    s.meta[id].created,
	}
	s.records = append(s.records, rec)
	if err := s.appendIndexLocked(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// Revert restores the file for snapshotID to its turn baseline: a created file is
// deleted, otherwise the baseline bytes are written back. Idempotent-ish: a missing
// snapshot returns an error; a re-revert re-writes the baseline (harmless).
func (s *editStore) Revert(snapshotID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.meta[snapshotID]
	if !ok {
		return errors.New("unknown edit")
	}
	ws := s.workspaceLocked()
	abs, err := resolveForWrite(ws, m.relPath)
	if err != nil {
		return err
	}
	if m.created {
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else {
		base, err := s.readSnapshotLocked("base-" + snapshotID)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, base, 0o600); err != nil {
			return err
		}
	}
	s.markRevertedLocked(snapshotID)
	return nil
}

// Diff returns the review of snapshotID: baseline vs the turn's latest content.
func (s *editStore) Diff(snapshotID string) (EditDiff, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.meta[snapshotID]
	if !ok {
		return EditDiff{}, errors.New("unknown edit")
	}
	base, err := s.readSnapshotLocked("base-" + snapshotID)
	if err != nil {
		return EditDiff{}, err
	}
	after, err := s.readSnapshotLocked("after-" + snapshotID)
	if err != nil {
		return EditDiff{}, err
	}
	return EditDiff{RelPath: m.relPath, Created: m.created, Lines: diff.Unified(string(base), string(after), reviewDiffOptions)}, nil
}

// List returns every recorded edit (with the current reverted flag), for resume.
func (s *editStore) List() []EditRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]EditRecord, len(s.records))
	for i, r := range s.records {
		r.Reverted = s.meta[r.SnapshotID].reverted
		out[i] = r
	}
	return out
}

// --- locked helpers (caller holds mu) ---

func (s *editStore) writeSnapshotLocked(name string, data []byte) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, name), data, 0o600)
}

func (s *editStore) readSnapshotLocked(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.dir, name))
}

// indexRow is the on-disk shape of one edit; mirrors EditRecord plus nothing else.
type indexRow struct {
	SnapshotID string `json:"snapshotId"`
	ToolUseID  string `json:"toolUseId"`
	RelPath    string `json:"relPath"`
	Added      int    `json:"added"`
	Removed    int    `json:"removed"`
	Created    bool   `json:"created"`
	Reverted   bool   `json:"reverted"`
}

func (s *editStore) appendIndexLocked(rec EditRecord) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "index.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	row := indexRow{rec.SnapshotID, rec.ToolUseID, rec.RelPath, rec.Added, rec.Removed, rec.Created, false}
	b, _ := json.Marshal(row)
	_, err = f.Write(append(b, '\n'))
	return err
}

func (s *editStore) loadIndexLocked() {
	f, err := os.Open(filepath.Join(s.dir, "index.jsonl"))
	if err != nil {
		return // no prior edits
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	max := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var row indexRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		s.records = append(s.records, EditRecord{
			SnapshotID: row.SnapshotID, ToolUseID: row.ToolUseID, RelPath: row.RelPath,
			Added: row.Added, Removed: row.Removed, Created: row.Created, Reverted: row.Reverted,
		})
		m := s.meta[row.SnapshotID]
		m.relPath = row.RelPath
		m.created = row.Created
		if row.Reverted {
			m.reverted = true
		}
		s.meta[row.SnapshotID] = m
		if n, err := strconv.Atoi(row.SnapshotID); err == nil && n >= max {
			max = n
		}
	}
	s.nextID = max + 1
}

// markRevertedLocked flips the reverted flag for snapshotID in memory and rewrites
// the index so it survives reopen.
func (s *editStore) markRevertedLocked(snapshotID string) {
	m := s.meta[snapshotID]
	m.reverted = true
	s.meta[snapshotID] = m
	for i := range s.records {
		if s.records[i].SnapshotID == snapshotID {
			s.records[i].Reverted = true
		}
	}
	// Rewrite index.jsonl from records (small).
	var b strings.Builder
	for _, r := range s.records {
		row := indexRow{r.SnapshotID, r.ToolUseID, r.RelPath, r.Added, r.Removed, r.Created, s.meta[r.SnapshotID].reverted}
		j, _ := json.Marshal(row)
		b.Write(j)
		b.WriteByte('\n')
	}
	_ = os.WriteFile(filepath.Join(s.dir, "index.jsonl"), []byte(b.String()), 0o600)
}

// readCapped reads path if it exists and is within the size cap. Returns
// (content, existed, ok). ok=false means skip (oversized or read error).
func readCapped(path string) (content []byte, existed bool, ok bool) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, false, true
	}
	if err != nil || info.IsDir() || info.Size() > maxEditSnapshotBytes {
		return nil, false, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, false
	}
	return data, true, true
}

// resolveForWrite resolves a workspace-relative path to an absolute path for
// writing/deleting, tolerating a not-yet-existing target: it rejects lexical
// escapes and verifies the nearest existing ancestor resolves within ws (symlink
// safe), mirroring toolpath.ResolveMutationTarget. Fail-closed.
func resolveForWrite(ws, rel string) (string, error) {
	if ws == "" {
		return "", errors.New("no workspace")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || filepath.IsAbs(clean) {
		return "", errors.New("path escapes workspace")
	}
	abs := filepath.Join(ws, clean)
	anc := abs
	for {
		if _, err := os.Lstat(anc); err == nil {
			break
		}
		parent := filepath.Dir(anc)
		if parent == anc {
			break
		}
		anc = parent
	}
	realAnc, err := filepath.EvalSymlinks(anc)
	if err != nil {
		return "", err
	}
	realWs, err := filepath.EvalSymlinks(ws)
	if err != nil {
		return "", err
	}
	if realAnc != realWs && !strings.HasPrefix(realAnc, realWs+string(os.PathSeparator)) {
		return "", errors.New("path escapes workspace")
	}
	return abs, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop/ -run TestEditStore -v`
Expected: PASS（全部 5 个用例）。

- [ ] **Step 5: 边界回归测试（越界写回必须拒绝）**

在 `editstore_test.go` 追加：

```go
func TestResolveForWriteRejectsEscape(t *testing.T) {
	ws := t.TempDir()
	for _, rel := range []string{"../evil.txt", "..", "a/../../evil"} {
		if _, err := resolveForWrite(ws, rel); err == nil {
			t.Fatalf("resolveForWrite(%q) should reject escape", rel)
		}
	}
	if _, err := resolveForWrite(ws, "sub/new.txt"); err != nil {
		t.Fatalf("resolveForWrite of a contained (missing) path should succeed, got %v", err)
	}
}
```

Run: `go test ./internal/desktop/ -run 'TestEditStore|TestResolveForWrite' -v`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
gofmt -w internal/desktop/ && go build ./...
git add internal/desktop/editstore.go internal/desktop/editstore_test.go
git commit -m "desktop: editStore — pre/post snapshots for undo + review" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: 桌面绑定 + 接线（App）

**Files:**
- Modify: `internal/desktop/app.go`
- Modify: `internal/desktop/attachments.go`
- Test: `internal/desktop/editstore_binding_test.go` (create)

**Interfaces:**
- Consumes: `editStore`（Task 4）。
- Produces（`*App` 导出方法，Wails 自动暴露）：
  ```go
  func (a *App) RevertEdit(snapshotID string) error
  func (a *App) ReviewEdit(snapshotID string) (EditDiff, error)
  func (a *App) ListEdits() []EditRecord
  ```
- `App` 新增字段 `edits *editStore`（在 `New` 里创建）；`buildAndSetLocked` 把它作为 `EditRecorder` 传入 `engine.Build`，并在 Build 成功后 `a.edits.BeginSession(cfg.CWD, session.SessionID())`；`SendMessage`/`SendMessageWithImages` 在启动回合前 `a.edits.BeginTurn()`。

- [ ] **Step 1: 写失败测试（绑定层）**

创建 `internal/desktop/editstore_binding_test.go`——直接构造一个绑定了 editStore 的 App，验证 List/Review/Revert 委托正确。

```go
package desktop

import (
	"path/filepath"
	"testing"
)

func TestAppEditBindingsDelegate(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.md"), "old\n")
	a := New(nil)
	a.workspace = ws
	a.edits.BeginSession(ws, "sess1")
	a.edits.BeginTurn()
	rec := simulateEdit(t, a.edits, ws, "a.md", "tu1", "new\n")

	if got := a.ListEdits(); len(got) != 1 || got[0].SnapshotID != rec.SnapshotID {
		t.Fatalf("ListEdits = %+v", got)
	}
	d, err := a.ReviewEdit(rec.SnapshotID)
	if err != nil || d.RelPath != "a.md" || len(d.Lines) == 0 {
		t.Fatalf("ReviewEdit = %+v err=%v", d, err)
	}
	if err := a.RevertEdit(rec.SnapshotID); err != nil {
		t.Fatal(err)
	}
	list := a.ListEdits()
	if len(list) != 1 || !list[0].Reverted {
		t.Fatalf("after revert, ListEdits = %+v", list)
	}
}
```

> `New(nil)` 传 nil sink 只用于绑定层测试（这些方法不触 sink）。若 `New` 对 nil sink 不安全，改为构造 `&App{sink: nil, edits: newEditStore()}` 直接字段赋值。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop/ -run TestAppEditBindings -v`
Expected: FAIL —— `a.edits` 字段与三个方法未定义。

- [ ] **Step 3: `App` 加字段 + 在 `New` 里创建**

`internal/desktop/app.go`：`App` 结构体（line 46-67）在 `previewURL string` 后加：

```go
	// edits captures Write/Edit pre/post content for the "已编辑" cards' undo/review.
	// One instance for the App's lifetime, rebound to each session via BeginSession.
	edits *editStore
```

`New`（line 70-72）改为：

```go
func New(sink EventSink) *App { return &App{sink: sink, edits: newEditStore()} }
```

- [ ] **Step 4: `buildAndSetLocked` 注入 recorder + 绑定会话**

`buildAndSetLocked`：把 editStore 作为 `EditRecorder` 传进 `engine.Build`（line 128-135）：

```go
	session, err := engine.Build(cfg, engine.Options{
		Permissions:    permSvc,
		StreamDelta:    func(delta string) { a.sink.Emit(EventAssistantDelta, AssistantDelta{Text: delta}) },
		StreamThinking: func(delta string) { a.sink.Emit(EventAssistantThinking, AssistantDelta{Text: delta}) },
		ToolEvents:     toolEvents,
		Warn:           warnWriter{sink: a.sink},
		ExtraTools:     []tool.Tool{preview.New()},
		EditRecorder:   a.edits,
	})
	if err != nil {
		return SessionInfo{}, err
	}
```

在 `a.config = cfg`（line 147）之后加绑定：

```go
	a.config = cfg
	a.edits.BeginSession(cfg.CWD, session.SessionID())
```

- [ ] **Step 5: `SendMessage`/`SendMessageWithImages` 起始处 `BeginTurn`**

`app.go` `SendMessage`：在 `a.inFlight = true`（line 176）之后、`a.mu.Unlock()`（line 177）之前加：

```go
	a.inFlight = true
	a.edits.BeginTurn()
	a.mu.Unlock()
```

`attachments.go` `SendMessageWithImages`：同样在 `a.inFlight = true`（line 53）之后、`a.mu.Unlock()`（line 54）之前加 `a.edits.BeginTurn()`。

- [ ] **Step 6: 加三个绑定方法**

在 `internal/desktop/editstore.go` 末尾（或 app.go）加：

```go
// RevertEdit restores the file for snapshotID to its turn baseline (Wails binding).
func (a *App) RevertEdit(snapshotID string) error { return a.edits.Revert(snapshotID) }

// ReviewEdit returns the baseline-vs-latest red/green diff for snapshotID (Wails binding).
func (a *App) ReviewEdit(snapshotID string) (EditDiff, error) { return a.edits.Diff(snapshotID) }

// ListEdits returns every recorded edit for the active session, so a resumed
// session can re-render its "已编辑" cards (Wails binding).
func (a *App) ListEdits() []EditRecord { return a.edits.List() }
```

- [ ] **Step 7: 跑测试确认通过 + 全量编译**

Run: `go test ./internal/desktop/ -run 'TestEditStore|TestAppEditBindings|TestResolveForWrite' -v && go build ./... && go -C cmd/runcode-desktop build ./...`
Expected: PASS；核心与桌面模块都编译通过。

- [ ] **Step 8: 提交**

```bash
gofmt -w internal/desktop/ && go build ./...
git add internal/desktop/app.go internal/desktop/attachments.go internal/desktop/editstore.go internal/desktop/editstore_binding_test.go
git commit -m "desktop: wire editStore into the session + RevertEdit/ReviewEdit/ListEdits bindings" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 6: 前端桥接类型与包装（bridge.ts）

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/bridge.ts`

**Interfaces:**
- Produces: `EditRecord`、`EditDiff` 类型；`revertEdit`/`reviewEdit`/`listEdits` 包装；`ResumedTool` 加可选 `data`。

- [ ] **Step 1: 加类型**

在 `bridge.ts` 的 `ToolEvent`（line 94 后）附近加：

```ts
// EditRecord is the per-edit metadata a Write/Edit tool event carries on `data`
// (live), and that ListEdits returns (resume). Anchored to a tool step by toolUseId.
export interface EditRecord {
  snapshotId: string
  toolUseId: string
  relPath: string
  added: number
  removed: number
  created: boolean
  reverted?: boolean
}

// EditDiff is the red/green review of one edit (turn baseline vs the turn's latest
// content for that file).
export interface EditDiff {
  relPath: string
  created: boolean
  lines: { stream?: string; text: string }[]
}

// isEditRecord narrows a ToolEvent's opaque `data` to an EditRecord (Write/Edit),
// distinguishing it from TodoWrite's PlanSnapshot.
export function isEditRecord(data: unknown): data is EditRecord {
  return !!data && typeof data === 'object' && 'snapshotId' in (data as object) && 'relPath' in (data as object)
}
```

- [ ] **Step 2: 加绑定包装**

在 `bridge.ts` 的绑定区（line 296-300 附近）加：

```ts
export const revertEdit = (snapshotId: string) => app().RevertEdit(snapshotId) as Promise<void>
export const reviewEdit = (snapshotId: string) => app().ReviewEdit(snapshotId) as Promise<EditDiff>
export const listEdits = () => app().ListEdits() as Promise<EditRecord[] | null>
```

- [ ] **Step 3: `ResumedTool` 加可选 `data`**

`ResumedTool`（line 137-143）加一行，让 resume 能挂上 EditRecord：

```ts
export interface ResumedTool {
  toolName?: string
  toolUseId?: string
  path?: string
  isError?: boolean
  output?: string
  // Attached client-side after resume (from ListEdits, by toolUseId) so an edited
  // file's card + undo/review re-render; not sent by the backend resume payload.
  data?: EditRecord
}
```

- [ ] **Step 4: 构建校验**

Run: `cd cmd/runcode-desktop/frontend && npm run build`
Expected: `✓ built`（纯类型/常量新增，无使用点尚不报错）。

- [ ] **Step 5: 提交**

```bash
git add cmd/runcode-desktop/frontend/src/bridge.ts
git commit -m "Desktop UI: bridge types + wrappers for edit undo/review" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 7: 预览标签支持 diff（preview-tabs 联合 + DiffPanel）

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/preview-tabs.ts`
- Modify: `cmd/runcode-desktop/frontend/src/preview-panel.tsx`
- Modify: `cmd/runcode-desktop/frontend/src/App.tsx`
- Test: `cmd/runcode-desktop/frontend/src/preview-tabs.test.ts` (create or extend)

**Interfaces:**
- Produces:
  ```ts
  export type PreviewTab = { kind: 'file'; relPath: string } | { kind: 'diff'; snapshotId: string; relPath: string }
  export function tabKey(t: PreviewTab): string
  export function openTab(tabs, active, tab: PreviewTab): { tabs; active: string }
  export function closeTab(tabs, active, key: string): { tabs; active: string | null }
  export function setActive(tabs, active, key: string): string | null
  ```
- 新组件 `DiffPanel`（preview-panel.tsx）渲染 `reviewEdit` 的红绿行。`PreviewPane` 按活动 tab 的 `kind` 分发到 `PreviewPanel` 或 `DiffPanel`。

- [ ] **Step 1: 写/改 preview-tabs 测试**

创建/扩展 `cmd/runcode-desktop/frontend/src/preview-tabs.test.ts`：

```ts
import { describe, it, expect } from 'vitest'
import { openTab, closeTab, tabKey, type PreviewTab } from './preview-tabs'

const file = (relPath: string): PreviewTab => ({ kind: 'file', relPath })
const diff = (snapshotId: string, relPath: string): PreviewTab => ({ kind: 'diff', snapshotId, relPath })

describe('preview-tabs union', () => {
  it('opens a file tab and focuses it', () => {
    const r = openTab([], null, file('a.md'))
    expect(r.tabs).toHaveLength(1)
    expect(r.active).toBe('a.md')
  })
  it('opens a diff tab keyed by snapshotId, distinct from the file tab', () => {
    const r1 = openTab([], null, file('a.md'))
    const r2 = openTab(r1.tabs, r1.active, diff('7', 'a.md'))
    expect(r2.tabs).toHaveLength(2)
    expect(r2.active).toBe('diff:7')
  })
  it('does not duplicate an already-open diff tab', () => {
    const r1 = openTab([], null, diff('7', 'a.md'))
    const r2 = openTab(r1.tabs, r1.active, diff('7', 'a.md'))
    expect(r2.tabs).toHaveLength(1)
  })
  it('closes by key and moves focus', () => {
    const t = [file('a.md'), diff('7', 'a.md')]
    const r = closeTab(t, 'diff:7', 'diff:7')
    expect(r.tabs).toHaveLength(1)
    expect(r.active).toBe('a.md')
  })
  it('tabKey distinguishes file vs diff', () => {
    expect(tabKey(file('a.md'))).toBe('a.md')
    expect(tabKey(diff('7', 'a.md'))).toBe('diff:7')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run preview-tabs`
Expected: FAIL —— 旧 `openTab` 签名是 `(tabs, active, relPath)`，`tabKey` 不存在。

- [ ] **Step 3: 重写 preview-tabs.ts**

```ts
// A preview tab is either a workspace file (rendered in PreviewPanel) or an edit
// review (rendered in DiffPanel). Tabs are keyed by tabKey: the relPath for files,
// "diff:<snapshotId>" for reviews, so a file and its diff coexist as two tabs.
export type PreviewTab =
  | { kind: 'file'; relPath: string }
  | { kind: 'diff'; snapshotId: string; relPath: string }

export function tabKey(t: PreviewTab): string {
  return t.kind === 'diff' ? 'diff:' + t.snapshotId : t.relPath
}

// openTab appends tab (focused), or just focuses it if a tab with the same key is
// already open.
export function openTab(tabs: PreviewTab[], _active: string | null, tab: PreviewTab): { tabs: PreviewTab[]; active: string } {
  const key = tabKey(tab)
  if (tabs.some((t) => tabKey(t) === key)) return { tabs, active: key }
  return { tabs: [...tabs, tab], active: key }
}

// closeTab removes the tab with the given key. If it was active, focus moves to the
// right neighbor, else the left, else null.
export function closeTab(tabs: PreviewTab[], active: string | null, key: string): { tabs: PreviewTab[]; active: string | null } {
  const idx = tabs.findIndex((t) => tabKey(t) === key)
  if (idx === -1) return { tabs, active }
  const next = tabs.filter((t) => tabKey(t) !== key)
  if (active !== key) return { tabs: next, active }
  const neighbor = next[idx] ?? next[idx - 1] ?? null
  return { tabs: next, active: neighbor ? tabKey(neighbor) : null }
}

// setActive focuses the tab with key if open, otherwise leaves active unchanged.
export function setActive(tabs: PreviewTab[], active: string | null, key: string): string | null {
  return tabs.some((t) => tabKey(t) === key) ? key : active
}
```

- [ ] **Step 4: preview-panel.tsx —— 加 `DiffPanel` + `PreviewPane` 分发**

在 `preview-panel.tsx` 顶部 import 处补：

```tsx
import { reviewEdit, type EditDiff } from './bridge'
import { type PreviewTab, tabKey } from './preview-tabs'
```

（`PreviewTabs`/`PreviewPane` 若已 `import { type PreviewTab } ...`，合并即可，勿重复。）

在 `PreviewPane` 之前加 `DiffPanel`：

```tsx
// DiffPanel renders the red/green review of one edit (baseline vs the turn's latest
// content), fetched via ReviewEdit. Reuses the diff-line CSS classes (.cl.diff_*).
function DiffPanel({ snapshotId, relPath, onClose }: { snapshotId: string; relPath: string; onClose: () => void }) {
  const [diff, setDiff] = useState<EditDiff | null>(null)
  const [err, setErr] = useState('')
  const name = relPath.replace(/\\/g, '/').split('/').pop() || relPath
  useEffect(() => {
    let ignore = false
    setDiff(null)
    setErr('')
    reviewEdit(snapshotId)
      .then((d) => { if (!ignore) setDiff(d) })
      .catch((e) => { if (!ignore) setErr(String(e)) })
    return () => { ignore = true }
  }, [snapshotId])
  return (
    <div className="flex flex-col h-full min-h-0 bg-surface">
      <div className="flex-none flex items-center gap-1.5 h-[44px] px-2.5 border-b border-line2">
        <Icon name="diff" size={15} className="flex-none text-muted" />
        <span className="flex-none text-[10.5px] text-faint bg-inset rounded px-1.5 py-0.5 mr-auto">审核 · {name}</span>
        <IconBtn name="win-close" title="关闭" onClick={onClose} />
      </div>
      <div className="flex-1 min-h-0 overflow-auto py-2 font-mono text-[12.5px] leading-[1.6]">
        {err && <div className="p-6 text-[13px] text-red">{err}</div>}
        {diff && diff.lines.length === 0 && <div className="p-6 text-[13px] text-muted">无差异。</div>}
        {diff && diff.lines.map((l, i) => (
          <div key={i} className={(l.stream || '').startsWith('diff') ? `cl ${l.stream}` : 'px-2.5 whitespace-pre text-muted'}>{l.text}</div>
        ))}
      </div>
    </div>
  )
}
```

把 `PreviewPane` 改为按 `kind` 分发（原实现按 `active` 直接渲染 `PreviewPanel`）：

```tsx
// PreviewPane composes the tab strip with the active tab's panel (file or diff).
export function PreviewPane({ tabs, active, baseURL, onSelect, onClose, onCloseTab }: { tabs: PreviewTab[]; active: string | null; baseURL: string; onSelect: (key: string) => void; onClose: () => void; onCloseTab: (key: string) => void }) {
  const activeTab = tabs.find((t) => tabKey(t) === active) ?? null
  return (
    <div className="flex flex-col h-full min-h-0">
      <PreviewTabs tabs={tabs} active={active} onSelect={onSelect} onClose={onCloseTab} />
      <div className="flex-1 min-h-0">
        {activeTab?.kind === 'file' && <PreviewPanel key={active} baseURL={baseURL} relPath={activeTab.relPath} onClose={onClose} />}
        {activeTab?.kind === 'diff' && <DiffPanel key={active} snapshotId={activeTab.snapshotId} relPath={activeTab.relPath} onClose={onClose} />}
      </div>
    </div>
  )
}
```

`PreviewTabs` 改为按 `tabKey` 选中/关闭（原来用 `t.relPath`）：

```tsx
export function PreviewTabs({ tabs, active, onSelect, onClose }: { tabs: PreviewTab[]; active: string | null; onSelect: (key: string) => void; onClose: (key: string) => void }) {
  return (
    <div className="flex-none flex items-stretch h-[36px] border-b border-line2 overflow-x-auto bg-surface">
      {tabs.map((t) => {
        const key = tabKey(t)
        const name = (t.kind === 'diff' ? '⇄ ' : '') + (t.relPath.replace(/\\/g, '/').split('/').pop() || t.relPath)
        const on = key === active
        return (
          <div
            key={key}
            onClick={() => onSelect(key)}
            title={t.relPath}
            className={`group flex items-center gap-1.5 pl-3 pr-2 max-w-[180px] flex-none cursor-pointer border-r border-line2 text-[12.5px] ${on ? 'bg-surface2 text-ink' : 'text-muted hover:bg-surface2/60'}`}
          >
            <span className="truncate">{name}</span>
            <button
              className="flex-none w-4 h-4 flex items-center justify-center rounded opacity-0 group-hover:opacity-100 hover:bg-line2 hover:text-ink"
              onClick={(e) => { e.stopPropagation(); onClose(key) }}
            >
              <Icon name="win-close" size={10} />
            </button>
          </div>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 5: App.tsx —— 更新 tab 调用点**

`App.tsx` `openArtifact`（line 337-342）改为传 file tab；新增 `openDiffTab`；`closePreviewTab` 改为按 key：

```tsx
  const openArtifact = (rel: string) => {
    const r = openTab(tabs, activeTab, { kind: 'file', relPath: rel })
    setTabs(r.tabs)
    setActiveTab(r.active)
    setBrowseOpen(false)
  }
  const openDiffTab = (snapshotId: string, relPath: string) => {
    const r = openTab(tabs, activeTab, { kind: 'diff', snapshotId, relPath })
    setTabs(r.tabs)
    setActiveTab(r.active)
    setBrowseOpen(false)
  }
```

`closePreviewTab`（line 351-355）——参数改成 key：

```tsx
  const closePreviewTab = (key: string) => {
    const r = closeTab(tabs, activeTab, key)
    setTabs(r.tabs)
    setActiveTab(r.active)
  }
```

`autoOpened` 判断（`ReplyArtifacts` 里 `tabs.some((t) => t.relPath === p)`，line 1575）——只 file tab 有 relPath：

```tsx
        <ArtifactCard key={p} relPath={p} add={0} del={0} onOpen={onOpen} autoOpened={tabs.some((t) => t.kind === 'file' && t.relPath === p)} />
```

> 实现者：搜索 App.tsx 里所有 `t.relPath ===`、`PreviewPane`/`PreviewTabs` 的 props 传递（`onSelect`/`onClose`/`onCloseTab` 现在收 key 字符串，与 `setActiveTab`/`closePreviewTab` 对齐——`onSelect={setActiveTab}`、`onCloseTab={closePreviewTab}` 即可）。确保 `openTab`/`closeTab` 的所有调用都用新签名。

- [ ] **Step 6: 加 `diff` 图标**

`icons.tsx` 在 `default:` 前加：

```tsx
    case 'diff':
      return (
        <svg {...common} {...stroke}>
          <path d="M12 4v6M9 7h6" />
          <path d="M9 17h6" />
        </svg>
      )
```

- [ ] **Step 7: 跑测试 + 构建**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run preview-tabs && npm run build`
Expected: preview-tabs 测试全绿；`✓ built`（无类型使用点残留旧签名）。

- [ ] **Step 8: 提交**

```bash
git add cmd/runcode-desktop/frontend/src/preview-tabs.ts cmd/runcode-desktop/frontend/src/preview-panel.tsx cmd/runcode-desktop/frontend/src/App.tsx cmd/runcode-desktop/frontend/src/preview-tabs.test.ts cmd/runcode-desktop/frontend/src/icons.tsx
git commit -m "Desktop UI: preview tabs support a diff/review tab (DiffPanel)" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 8: `edits` 渲染组（chat.ts）

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/chat.ts`
- Test: `cmd/runcode-desktop/frontend/src/chat.test.ts` (extend)

**Interfaces:**
- Consumes: `EditRecord`、`isEditRecord`（bridge.ts）。
- Produces: `Group` 联合新增 `{ kind: 'edits'; id: string; edits: EditRecord[] }`；`groupBlocks` 把每回合内带 edit `data` 的 Write/Edit 工具块归并成一个 `edits` 组（按 relPath 去重、保留最新），置于该回合末尾（下一个 user 块之前 / 数组结束）。Write/Edit 工具块仍照常进入 `exec` 组（紧凑步骤不受影响）。

- [ ] **Step 1: 写失败测试**

在 `chat.test.ts` 追加（若无该文件则创建，import 现有 `groupBlocks`/`Block`）：

```ts
import { describe, it, expect } from 'vitest'
import { groupBlocks, mergeTool, type Block } from './chat'
import type { EditRecord, ToolEvent } from './bridge'

function editTool(id: string, tuid: string, rel: string, snapshotId: string, added: number): Block {
  const rec: EditRecord = { snapshotId, toolUseId: tuid, relPath: rel, added, removed: 0, created: false }
  return { kind: 'tool', id, tool: { type: 'completed', toolName: 'Write', toolUseID: tuid, data: rec } }
}

describe('mergeTool preserves edit metadata', () => {
  it('carries data from the completed event onto the started block', () => {
    const started: ToolEvent = { type: 'started', toolName: 'Write', toolUseID: 'tu1', input: {} }
    const rec: EditRecord = { snapshotId: '1', toolUseId: 'tu1', relPath: 'a.md', added: 3, removed: 0, created: true }
    const completed: ToolEvent = { type: 'completed', toolName: 'Write', toolUseID: 'tu1', data: rec }
    expect(mergeTool(started, completed).data).toEqual(rec)
  })
})

describe('groupBlocks edits group', () => {
  it('emits one edits group per turn, one card per file, at the turn end', () => {
    const blocks: Block[] = [
      { kind: 'user', id: 'u1', text: 'go', ts: '' },
      editTool('t1', 'tu1', 'a.md', '1', 3),
      { kind: 'assistant', id: 'a1', text: 'done', streaming: false, ts: '' },
    ]
    const g = groupBlocks(blocks)
    const edits = g.filter((x) => x.kind === 'edits')
    expect(edits).toHaveLength(1)
    expect(edits[0].kind === 'edits' && edits[0].edits.map((e) => e.relPath)).toEqual(['a.md'])
    // The edits group comes after the assistant block.
    const idxEdits = g.findIndex((x) => x.kind === 'edits')
    const idxAsst = g.findIndex((x) => x.kind === 'block' && x.block.kind === 'assistant')
    expect(idxEdits).toBeGreaterThan(idxAsst)
    // The Write step is still present in an exec group.
    expect(g.some((x) => x.kind === 'exec')).toBe(true)
  })

  it('dedupes two edits to the same file in a turn, keeping the latest stat', () => {
    const blocks: Block[] = [
      { kind: 'user', id: 'u1', text: 'go', ts: '' },
      editTool('t1', 'tu1', 'a.md', '1', 3),
      editTool('t2', 'tu2', 'a.md', '1', 5), // same baseline id, later stat
    ]
    const g = groupBlocks(blocks)
    const edits = g.find((x) => x.kind === 'edits')
    expect(edits && edits.kind === 'edits' && edits.edits).toHaveLength(1)
    expect(edits && edits.kind === 'edits' && edits.edits[0].added).toBe(5)
  })

  it('separates edits across two turns', () => {
    const blocks: Block[] = [
      { kind: 'user', id: 'u1', text: 'go', ts: '' },
      editTool('t1', 'tu1', 'a.md', '1', 3),
      { kind: 'user', id: 'u2', text: 'again', ts: '' },
      editTool('t2', 'tu2', 'b.md', '2', 4),
    ]
    const g = groupBlocks(blocks)
    expect(g.filter((x) => x.kind === 'edits')).toHaveLength(2)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run chat`
Expected: FAIL —— `edits` 组不存在。

- [ ] **Step 3: 实现**

`chat.ts` `Group` 类型（line 21-25）加一支：

```ts
export type Group =
  | { kind: 'block'; block: Block }
  | { kind: 'exec'; id: string; tools: ToolEvent[] }
  | { kind: 'ask'; id: string; tool: ToolEvent }
  | { kind: 'analyze'; id: string; tool: ToolEvent }
  | { kind: 'edits'; id: string; edits: EditRecord[] }
```

顶部 import 加 `EditRecord`/`isEditRecord`：

```ts
import type { ToolEvent, PlanSnapshot, PlanItem, EditRecord } from './bridge'
import { isEditRecord } from './bridge'
```

重写 `groupBlocks`（line 84-121）加入 per-turn edit 归并 + 回合末尾 flush：

```ts
export function groupBlocks(blocks: Block[]): Group[] {
  const out: Group[] = []
  // Per-turn edited files, deduped by relPath (latest wins). Flushed as one `edits`
  // group at the end of each turn (before the next user block, or at the end), so
  // the cards sit under the reply — matching the artifact cards' placement.
  let pending = new Map<string, EditRecord>()
  let pendingId = ''
  const flush = () => {
    if (pending.size === 0) return
    out.push({ kind: 'edits', id: 'edits-' + pendingId, edits: [...pending.values()] })
    pending = new Map()
    pendingId = ''
  }
  for (const b of blocks) {
    if (b.kind === 'user') {
      flush() // end of the previous turn
      out.push({ kind: 'block', block: b })
      continue
    }
    if (b.kind === 'tool') {
      // A Write/Edit that carries edit metadata also contributes an "已编辑" card.
      if (isEditRecord(b.tool.data)) {
        const rec = b.tool.data as EditRecord
        pending.set(rec.relPath, rec)
        if (!pendingId) pendingId = b.id
      }
      if (b.tool.toolName === 'TodoWrite') continue
      if (b.tool.toolName === 'AskUser') { out.push({ kind: 'ask', id: b.id, tool: b.tool }); continue }
      if (b.tool.toolName === 'Analyze') { out.push({ kind: 'analyze', id: b.id, tool: b.tool }); continue }
      if (b.tool.toolName === 'Task') { out.push({ kind: 'block', block: b }); continue }
      const last = out[out.length - 1]
      if (last && last.kind === 'exec') last.tools.push(b.tool)
      else out.push({ kind: 'exec', id: b.id, tools: [b.tool] })
      continue
    }
    out.push({ kind: 'block', block: b })
  }
  flush() // trailing turn
  return out
}
```

> 注意：`flush` 在遇到下一个 user 块前触发，把 `edits` 组放在该回合所有 assistant/exec 之后——即回合末尾。`exec` 归并逻辑保持原样（Write/Edit 仍出现在紧凑步骤里）。

- [ ] **Step 3b: `mergeTool` 保留 `data`（关键）**

`mergeTool`（`chat.ts` line 66-81 的 return）**当前不带 `data`**——`completed` 事件的 EditRecord 会在合并到 `started` 块时丢失，导致 live 编辑卡拿不到元数据。在 return 对象里加一行（放在 `outputTruncated` 之后即可）：

```ts
  return {
    ...prev,
    type: ev.type,
    toolName: ev.toolName || prev.toolName,
    input: ev.input ?? prev.input,
    message: ev.message || prev.message,
    files: ev.files?.length ? ev.files : prev.files,
    filesTotal: ev.filesTotal ?? prev.filesTotal,
    output,
    outputTotal: ev.outputTotal ?? prev.outputTotal,
    outputTruncated: ev.outputTruncated ?? prev.outputTruncated,
    // Side-channel payload (edit metadata / plan snapshot) arrives on the final
    // event; keep whichever event carries it so the edited card survives the merge.
    data: ev.data ?? prev.data,
  }
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run chat`
Expected: PASS（新 `mergeTool` + 3 个 `edits` 例 + 既有 chat 测试不回归）。

- [ ] **Step 5: 提交**

```bash
git add cmd/runcode-desktop/frontend/src/chat.ts cmd/runcode-desktop/frontend/src/chat.test.ts
git commit -m "Desktop UI: groupBlocks emits a per-turn edits group from edit metadata" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 9: 「已编辑」卡片组件 + 渲染 + 撤销/审核接线 + resume 挂载（App.tsx）

**Files:**
- Create: `cmd/runcode-desktop/frontend/src/edited-card.tsx`
- Modify: `cmd/runcode-desktop/frontend/src/App.tsx`

**Interfaces:**
- Consumes: `EditRecord`（bridge）、`revertEdit`/`listEdits`（bridge）、`openDiffTab`（Task 7）。
- Produces: `EditedCards({ edits, reverted, onReview, onUndo })`；App 里 `revertedEdits: Set<string>` 状态、`handleUndo`、resume 时 `listEdits()` 按 `toolUseId` 挂回工具块。

- [ ] **Step 1: 新建 `edited-card.tsx`**

```tsx
import { useState } from 'react'
import { Icon } from './icons'
import type { EditRecord } from './bridge'

// EditedCard is the "已编辑" card for one edited file: an edit icon, the filename,
// the accurate +N -N, and 撤销 / 审核 actions. Undo uses an inline confirm (no
// native dialog) — cross-platform and on-brand. A reverted card goes grey.
function EditedCard({ rec, reverted, onReview, onUndo }: { rec: EditRecord; reverted: boolean; onReview: () => void; onUndo: () => void }) {
  const [confirming, setConfirming] = useState(false)
  const name = rec.relPath.replace(/\\/g, '/').split('/').pop() || rec.relPath
  return (
    <div className={`flex items-center gap-2.5 border border-line2 rounded-lg pl-3 pr-2.5 py-2 bg-surface ${reverted ? 'opacity-60' : ''}`}>
      <span className="flex-none text-muted"><Icon name="file-edit" size={17} /></span>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 text-[13px] font-medium text-ink font-mono truncate" title={rec.relPath}>
          <span className="text-faint font-sans font-normal text-[11.5px] flex-none">已编辑</span>
          {name}
        </div>
        <div className="text-[11px] font-mono">
          <span className="text-green">+{rec.added}</span> <span className={rec.removed > 0 ? 'text-red' : 'text-faint'}>−{rec.removed}</span>
          {rec.created && <span className="text-faint ml-1.5">新建</span>}
        </div>
      </div>
      {reverted ? (
        <span className="flex-none text-[11px] text-faint">已撤销</span>
      ) : confirming ? (
        <span className="flex-none flex items-center gap-1.5 text-[12px]">
          <span className="text-muted">确认撤销?</span>
          <button className="text-red hover:underline" onClick={() => { setConfirming(false); onUndo() }}>是</button>
          <button className="text-muted hover:underline" onClick={() => setConfirming(false)}>否</button>
        </span>
      ) : (
        <span className="flex-none flex items-center gap-2.5 text-[12px] text-muted">
          <button className="hover:text-ink flex items-center gap-1" onClick={() => setConfirming(true)}>撤销 <Icon name="undo" size={12} /></button>
          <button className="hover:text-ink" onClick={onReview}>审核</button>
        </span>
      )}
    </div>
  )
}

// EditedCards renders one EditedCard per edited file in a turn.
export function EditedCards({ edits, reverted, onReview, onUndo }: { edits: EditRecord[]; reverted: Set<string>; onReview: (snapshotId: string, relPath: string) => void; onUndo: (snapshotId: string) => void }) {
  if (edits.length === 0) return null
  return (
    <div className="flex flex-col gap-1.5 mt-1.5">
      {edits.map((e) => (
        <EditedCard key={e.toolUseId} rec={e} reverted={reverted.has(e.snapshotId)} onReview={() => onReview(e.snapshotId, e.relPath)} onUndo={() => onUndo(e.snapshotId)} />
      ))}
    </div>
  )
}
```

- [ ] **Step 2: 加图标 `file-edit` 与 `undo`**

`icons.tsx` 在 `default:` 前加（`file-edit` = 文档 + 铅笔角；`undo` = 回转箭头）：

```tsx
    case 'file-edit':
      return (
        <svg {...common} {...stroke}>
          <path d="M13 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-8" />
          <path d="M16.5 3.5a1.6 1.6 0 0 1 2.3 2.3L14 10.6l-3 .7.7-3z" />
        </svg>
      )
    case 'undo':
      return (
        <svg {...common} {...stroke}>
          <path d="M9 7 4 12l5 5" />
          <path d="M4 12h11a5 5 0 0 1 0 10h-1" />
        </svg>
      )
```

- [ ] **Step 3: App.tsx —— 状态 + 撤销处理 + 渲染 edits 组**

(a) import（line 62 附近）：

```tsx
import { EditedCards } from './edited-card'
import { listEdits, revertEdit } from './bridge'
```

（`revertEdit`/`listEdits` 已在 bridge，合并 import。）

(b) 状态（`tabs`/`activeTab` 附近，line 318-319 后）：

```tsx
  const [revertedEdits, setRevertedEdits] = useState<Set<string>>(new Set())
```

(c) 撤销处理（`openArtifact`/`openDiffTab` 附近）：

```tsx
  const handleUndo = async (snapshotId: string) => {
    try {
      await revertEdit(snapshotId)
      setRevertedEdits((s) => new Set(s).add(snapshotId))
      listFiles().then((f) => setFiles(f ?? [])).catch(() => {})
    } catch (e) {
      setBlocks((prev) => [...prev, { kind: 'warning', id: nextID(), text: '撤销失败：' + String(e) }])
    }
  }
```

(d) 渲染分支：在 `groups.map` 的三元链里（line 1112-1132），在 `g.kind === 'analyze'` 之后加一支：

```tsx
              ) : g.kind === 'edits' ? (
                <BotRow key={g.id}><EditedCards edits={g.edits} reverted={revertedEdits} onReview={openDiffTab} onUndo={handleUndo} /></BotRow>
              ) : g.kind === 'analyze' ? (
```

> 放在 `analyze` 之前或之后都可，只要在终端 `else`（block 分支）之前。保持 `key={g.id}`。

- [ ] **Step 4: resume —— 按 toolUseId 挂回 EditRecord**

`openRecent`（line 943-984）在 `setBlocks(...)` 之后追加：拉取 `listEdits`，把每条 EditRecord 按 `toolUseId` 挂到对应工具块的 `tool.data`，并用其 `reverted` 播种 `revertedEdits`。

在 `openRecent` 里 `setBlocks(...)` 调用**之后**插入：

```tsx
      // Re-attach edit metadata to resumed tool steps by tool-use id, so the "已编辑"
      // cards + undo/review re-render (the resume payload itself carries no diffs).
      const edits = (await listEdits()) ?? []
      if (edits.length > 0) {
        const byTUID = new Map(edits.map((e) => [e.toolUseId, e]))
        setBlocks((prev) => prev.map((b) => {
          if (b.kind !== 'tool' || !b.tool.toolUseID) return b
          const e = byTUID.get(b.tool.toolUseID)
          return e ? { ...b, tool: { ...b.tool, data: e } } : b
        }))
        setRevertedEdits(new Set(edits.filter((e) => e.reverted).map((e) => e.snapshotId)))
      } else {
        setRevertedEdits(new Set())
      }
```

> `groupBlocks` 随后会从这些 `tool.data` 生成 `edits` 组，与 live 完全一致。因为 resume 的 tool 块由 `mergeTool` 之外的直接映射构建，`data` 字段会被 `groupBlocks` 读到。

- [ ] **Step 5: 构建 + 全部前端测试**

Run: `cd cmd/runcode-desktop/frontend && npm run build && npx vitest run`
Expected: `✓ built`；vitest 全绿（含新 preview-tabs、chat 用例）。

- [ ] **Step 6: 提交**

```bash
git add cmd/runcode-desktop/frontend/src/edited-card.tsx cmd/runcode-desktop/frontend/src/icons.tsx cmd/runcode-desktop/frontend/src/App.tsx
git commit -m "Desktop UI: 已编辑 cards with inline-confirm undo + review diff tab" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 10: 打包与手动验证

**Files:** none（构建 + 手动）。

- [ ] **Step 1: 全量门槛**

Run:
```bash
go build ./... && go test ./internal/diff/ ./internal/repl/ ./internal/desktop/ -count=1 && (cd cmd/runcode-desktop/frontend && npm run build && npx vitest run) && go -C cmd/runcode-desktop build ./...
```
Expected: 全绿。

- [ ] **Step 2: 打包**

Run: `cd cmd/runcode-desktop && wails build`
Expected: 产出 `cmd/runcode-desktop/build/bin/XRUN.exe`（Wails 会重生 JS 绑定，把 `RevertEdit`/`ReviewEdit`/`ListEdits` 暴露给前端）。

- [ ] **Step 3: 手动验证（开发机 OS）**

启动 XRUN，打开一个工作区：
- 让模型改一个已存在的 md → 该回合下方出「已编辑 <文件> +N −N」卡（`+N-N` 准确，非截断）；`审核` 打开右栏红绿 diff 标签，展示该回合的改动；`撤销` → 内联「确认撤销? 是/否」→ 点「是」文件复原、卡片转「已撤销」，工作区文件刷新。
- 让模型**新建**一个文件 → 卡显示「新建」；`撤销` = 删除该文件。
- 同一文件一回合内被改两次 → 只一张卡，`+N-N` 为累计，`撤销`回到该回合开始前。
- 关闭该会话再从历史重开（resume）→「已编辑」卡按文件复现，`审核`/`撤销` 仍可用；已撤销的显示「已撤销」。
- 跑 CLI（`go run ./cmd/runcode` 或已装二进制）做一次 Write/Edit → **无**「已编辑」卡、行为与统计不变（recorder 为 nil）。
- 越界健壮性：正常操作下不会触发；`resolveForWrite`/`resolveWithinWorkspace` 的单测已覆盖越界拒绝。

- [ ] **Step 4: 汇报结果（仅当有配置文件变更才提交；否则报告）**

---

## Notes for the implementer

- **不改 Write/Edit 工具**：捕获全在 executor 括弧 + 桌面 recorder。core 不为该 UI 功能做任何文件 IO（`BeginEdit`/`Commit` 的读写都在 `editStore`）。
- **shell-free / 边界 fail-closed 是硬不变量**：撤销写回/删除只走 `resolveForWrite`（容忍缺失目标、校验最近存在祖先在工作区内、symlink 安全），审核/读取走既有 `resolveWithinWorkspace`。任何越界一律拒绝。
- **回合基线语义**：`BeginTurn` 每个用户回合清空基线映射——同一文件在同一回合的多次编辑共享一个基线快照（撤销/审核=该回合累计），跨回合各自独立基线。
- **live 与 resume 同路**：edit 元数据始终经工具块 `tool.data` → `groupBlocks` 生成 `edits` 组。live 由事件带来，resume 由 `ListEdits` 按 `toolUseId` 挂回。无 `turnSeq` 对齐。
- **护栏**：单快照 > 4 MiB 跳过（不出卡）；审核 diff 有界（超限落「large file diff omitted」）；`diff.Stat` 超 5 万行退化为粗略估计。这些是刻意取舍，不是 bug。
- **撤销确认**：本轮用简洁的卡内联「确认撤销? 是/否」。spec 提过的「文件之后又被改过 → 确认文案额外提示」是**有意延后**的小增强（需在撤销时多读一次当前文件与 `after` 比对），不在本轮范围，避免每次撤销都多一次 IO；语义上撤销始终回到该回合基线。
- **不要动**用户未提交的 WIP 文档（README/CHANGELOG/architecture 等），不要合并到 main。
- **`.runcode/edits/`** 是会话侧车，跟 `.runcode/sessions/` 一致；不进版本库由既有 `.gitignore`/工作区约定覆盖（工作区非本仓，无需改本仓 .gitignore）。
```
