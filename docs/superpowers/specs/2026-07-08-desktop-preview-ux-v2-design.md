# 桌面产物预览 UX v2（卡片 + 多标签 + 多种打开方式 + 可缩放）

日期：2026-07-08
状态：已批准设计（据用户参考图重规划），待写实现计划

## 背景

产物预览子项目 A 已上线（右侧单文件预览面板 + 工具卡小"预览"按钮 + 文件浏览器）。用户反馈三点并给出参考图（Claude/Cursor 式产物交互）：

1. 预览面板样式太朴素（"太丑"）。
2. 生成的文件希望在**对话区域直接选中/打开**（现在只有工具卡上一个很小的"预览"文字按钮）。
3. 预览区**不能拖拽缩放**。

参考图还显示了两处成熟做法：对话区里文件以**卡片**呈现（图标 + 文件名 + 类型副标题 + "打开方式"下拉），预览区顶部是**多标签页**，右侧带**文件筛选/树**。本 spec 据此重规划。

## 范围

**做（v2）**：
- 对话区**产物卡片**：可预览的 Write/Edit 产物在对话流里以卡片呈现（类型图标、文件名、类型副标题、diff 数、"打开方式"下拉），点击即在右侧打开。
- 预览区**多标签页**：可同时打开多个文件成标签，聚焦/关闭/active 高亮；点已开文件则聚焦其标签。
- 预览面板**重新配色**：用 App 现有 `Icon` 组件替换裸字符工具栏，规整头部 + 类型徽章，正文沿用 App 排版。
- **可拖拽缩放**：对话区与预览区之间加可拖动分隔条，宽度带上下限并记住（localStorage）。
- **"打开方式"下拉**（四项）：`预览（面板内）` / `用系统默认程序打开` / `在文件夹中显示` / `复制路径`。
- 文件浏览器顶部加**筛选框**。
- **顺带清理**：把当前重复的"工作区边界校验"逻辑抽成一个共享 helper（新绑定 OpenExternal/RevealInFolder 也要用），消除 preview_server.go 与 preview.go 的两份拷贝。

**不做（YAGNI / 越界）**：
- 参考图里的"撤销 / 审核"（diff 评审 + 回滚）—— 那是独立功能，不属于预览。
- 拖拽调整标签顺序、标签持久化跨会话、分屏对比、在预览里编辑回写。
- Office/dev-server 预览（仍是后续子项目 B/C）。

## 架构

分成后端绑定、共享边界 helper、前端预览标签模型、前端组件四块，各自单一职责、可独立测试。

### 后端（Go，`internal/desktop`）

**共享边界 helper（新增，先做）**：`resolveWithinWorkspace(ws, relPath string) (resolved string, err error)`（落 `internal/desktop/workspacepath.go`）。做当前 `ReadArtifact` 那套：lexical `..` 校验 + `EvalSymlinks` 解析 + **fail-closed** 边界判定，返回已解析的绝对路径或错误。`ReadArtifact`、新的 `OpenExternal`/`RevealInFolder`/`ResolveArtifactPath` 都复用它；`preview_server.go` 的 `previewPathWithinRoot` 也改为基于它（保持只读服务用的 bool 语义）。这样"解析 + fail-closed + 越界拒绝"只有一份实现。

**新绑定（`internal/desktop/open.go`）**，全部经 `resolveWithinWorkspace` 限定在工作区内，再按 `runtime.GOOS` 调用 OS：
- `OpenExternal(relPath string) error` —— 用系统默认程序打开该文件。Windows `cmd /c start "" <abs>`；macOS `open <abs>`；Linux `xdg-open <abs>`。（注意：这是打开**真实文件**，不是现在用的 `BrowserOpenURL(loopbackURL)`——那个在浏览器里以 text/plain 显示，语义不符。）
- `RevealInFolder(relPath string) error` —— 在文件管理器中定位。Windows `explorer /select,<abs>`；macOS `open -R <abs>`；Linux `xdg-open <dir>`（无选中，退化为打开所在目录）。
- `ResolveArtifactPath(relPath string) (string, error)` —— 返回工作区内的绝对路径，供前端"复制路径"用。

安全：三者都拒绝越界/symlink 逃逸（复用 helper，fail-closed）。OS 启动本身不做单测（副作用），但**越界拒绝**做单测（helper 层 + 绑定在越界路径上返回错误、不启动）。

### 前端预览标签模型（新增）

`cmd/runcode-desktop/frontend/src/preview-tabs.ts`（纯逻辑，**可单测**）：
- 类型 `PreviewTab = { relPath: string }`（relPath 唯一即 key）。
- 纯 reducer 函数：
  - `openTab(tabs, active, relPath) => { tabs, active }` —— 已存在则只切 active，否则追加并 active。
  - `closeTab(tabs, active, relPath) => { tabs, active }` —— 移除；若关的是 active，active 落到相邻标签（右邻优先，否则左邻，否则 null）。
  - `setActive(tabs, active, relPath) => active`。
- App 持有 `[tabs, activeTab]` 两个 state，通过上面纯函数更新。右栏在 `browseOpen`（文件浏览器）与 `tabs.length>0`（标签预览）间切换渲染。

### 前端组件

- `artifact-card.tsx`（新增）：
  - `ArtifactCard({ relPath, verb, add, del, onOpen, baseURL, cwd })` —— 一张产物卡：类型图标（按 `classifyPreview` 的 kind 映射）、文件名（加粗）+ 类型副标题（如"Markdown 文档 / PNG 图像 / Python"）、编辑类右侧 `+add −del`、末尾"打开方式"下拉。整卡点击 = 面板内预览（`onOpen`）。
  - `OpenWithMenu({ relPath, baseURL, cwd, onPreview })` —— 四项下拉（预览 / 系统默认程序打开 / 在文件夹中显示 / 复制路径），分别调 `onPreview`、`openExternal(relPath)`、`revealInFolder(relPath)`、`resolveArtifactPath(relPath)→ClipboardSetText`。
  - `artifactKindLabel(kind): string` —— kind→中文类型副标题（纯函数，可单测）。
- `preview-panel.tsx`（改造）：
  - 新增 `PreviewTabs({ tabs, active, onSelect, onClose })` —— 顶部标签条。
  - `PreviewPane({ tabs, active, baseURL, cwd, onSelect, onClose, onCloseTab })` —— 组合标签条 + 当前标签的 `PreviewPanel`；`PreviewPanel` 头部工具栏改用 `Icon`（刷新/外部打开/复制路径/关闭）+ 类型徽章，正文与配色沿用 App 设计语言（`bg-surface`、圆角、`border-line2`、留白）。
  - `FileBrowser` 顶部加"筛选文件…"输入框，按子串过滤后再 `buildFileTree`。
- **可拖拽分隔条**：右栏 `<aside>` 左边缘加一个 6px 宽的 drag handle；`onPointerDown` 起拖，`pointermove` 改宽度 state，clamp 到 `[360, 60%窗口]`，松手写 localStorage（key 如 `preview.width`），初始从 localStorage 读。
- 图标：`icons.tsx` 新增 `refresh`、`external-link`、`copy`（工具栏/菜单用）；文件类型图标复用 `file`/`globe`，图片可用 `file`（或新增 `image`）。

### 对话区接线（App.tsx）

- 保留 ExecutionCard 的执行步骤列表（展示过程），但**去掉**其中 Write/Edit 行上的旧小"预览"按钮。
- 在一次 bot 回合的执行卡之后，渲染一个**产物卡组**：收集该回合 `type==='completed'` 且 `classifyPreview` 可预览的 Write/Edit 目标，去重后逐一渲染 `ArtifactCard`。卡的 `onOpen(relPath)` → `openTab` 打开/聚焦标签，并确保右栏可见。
- 右栏渲染：`view==='chat' && (tabs.length>0 || browseOpen)` 时显示 `<aside>`（可拖宽）；标签有值→`PreviewPane`，否则→`FileBrowser`（带筛选）。
- 路径统一经 `toWorkspaceRel(raw, info.cwd)` 归一后再进标签模型（与现状一致）。

### bridge / 类型

- `bridge.ts` 增 `openExternal`、`revealInFolder`、`resolveArtifactPath` 三个绑定；`wails.d.ts` 的 `App` 接口补三方法，`runtime` 接口补 `ClipboardSetText(text: string): Promise<boolean>`（Wails 运行时自带）。

## 数据流

```
AI 写/改文件 → 工具事件(带 path) → 该回合可预览产物 → 渲染 ArtifactCard(卡片)
点卡片(或"打开方式>预览") → openTab(relPath) → 右栏 PreviewPane 显示该标签
点已开文件 → openTab 命中已存在 → 仅切 active
关标签 → closeTab → active 落到相邻标签或 null(标签空则右栏回到文件浏览器/隐藏)
"打开方式>系统默认" → openExternal(relPath)      (OS 默认程序)
"打开方式>在文件夹显示" → revealInFolder(relPath)  (资源管理器定位)
"打开方式>复制路径" → resolveArtifactPath → ClipboardSetText(abs)
拖分隔条 → 宽度 state 改变 → 松手写 localStorage
```

## 类型与"打开方式"矩阵

| kind | 面板内预览 | 系统默认打开 | 文件夹显示 | 复制路径 |
|---|---|---|---|---|
| markdown/code/text/html/image/svg | ✓ | ✓ | ✓ | ✓ |
| unsupported（docx/zip…） | 置灰（提示用系统程序） | ✓ | ✓ | ✓ |

不可预览类型的卡片仍出现（可"打开方式>系统默认"），只是"预览"项置灰。

## 错误处理

- `openExternal`/`revealInFolder` 失败（越界被拒 / OS 报错）→ 前端 toast/内联提示，不崩。
- 越界 relPath → 后端 helper 直接返回错误，不启动任何进程。
- `ClipboardSetText` 不可用 → 回退 `navigator.clipboard.writeText`，再失败则提示。
- 标签指向的文件被删/读失败 → 该标签内容区显示错误 + 重试（沿用现有 PreviewPanel 错误分支）。
- 拖拽宽度越界 → clamp，不允许把对话区挤没。

## 测试计划

**后端（Go，TDD）**：
- `resolveWithinWorkspace`：工作区内文件返回解析后绝对路径；`..`/symlink/junction 逃逸被拒（fail-closed）；不存在的路径报错。迁移后 `ReadArtifact`、`previewPathWithinRoot` 行为不回归（现有测试仍绿，含 `..hidden` 与 junction 用例）。
- `OpenExternal`/`RevealInFolder`/`ResolveArtifactPath`：越界路径返回错误且**不**启动进程（用工作区外/junction 路径断言 error）；`ResolveArtifactPath` 对工作区内文件返回正确绝对路径。OS 启动副作用不单测。

**前端（vitest）**：
- `preview-tabs.ts`：`openTab` 去重/追加/切 active；`closeTab` 关 active 时 active 落到右邻→左邻→null；关非 active 不动 active。
- `artifactKindLabel`：各 kind → 正确中文副标题。
- `FileBrowser` 筛选纯函数（若抽出）：子串过滤命中/不命中。

**手动/构建**：`wails build` 出包，实测：对话区产物卡片、点开成标签、多标签切换/关闭、四种打开方式、拖拽缩放、文件筛选、窄窗可用。

## 落点（文件）

- 后端：`internal/desktop/workspacepath.go`（`resolveWithinWorkspace` + 测试）、`internal/desktop/open.go`（`OpenExternal`/`RevealInFolder`/`ResolveArtifactPath` + 测试）；改 `preview.go`、`preview_server.go` 复用 helper。
- 前端：`preview-tabs.ts`（+ 测试）、`artifact-card.tsx`、改 `preview-panel.tsx`（PreviewTabs/PreviewPane + 重配色 + 筛选）、改 `App.tsx`（产物卡组 + 标签 state + 可拖分隔条）、`bridge.ts`（三绑定）、`wails.d.ts`（三 App 方法 + ClipboardSetText）、`icons.tsx`（refresh/external-link/copy）。

## 已定决策

- 一次做全（含多标签）。
- "打开方式"= 预览 / 系统默认程序打开 / 在文件夹中显示 / 复制路径。
- 复制路径复制**绝对路径**（经后端解析，工作区内）。
- 宽度记忆用 localStorage（非 desktop.json）。
- 不做撤销/审核、不做标签持久化。
- 顺带把工作区边界校验抽成共享 helper（消除现有两份拷贝）。
