# 产物卡片改用正则识别 + `preview` 模型工具

日期：2026-07-09
状态：已批准设计（上轮 Q&A 确认：只用正则 / preview 仅桌面），待写实现计划

## 背景

产物功能第 5 轮。上轮已定两件事，本轮实现：
1. **卡片只用正则**：不再从 `Write`/`Edit` 工具事件生卡；改成扫 AI 回复正文里"像文件路径"的 token、只保留工作区真实存在的，去重成卡片。好处：AI 只是"提到"的文件也能点开；坏处：模型没在正文提的文件不出卡（用户已接受）。
2. **`preview` 模型工具**：给模型一个工具，生成文档/网站(H5)后主动调用它把成品推到桌面预览面板。**仅桌面注册，CLI 不暴露**。

跨平台仍是硬约束（Mac/Win/Linux）；工具侧改动是纯 Go、天然跨平台。

## 范围

**做**：#2 正则卡片（替换工具事件生卡）、#4 `preview` 工具（含引擎注入点 + 前端捕获）。
**不做**：改预览面板本身、Office/dev-server 预览、扫工具输出（本轮只扫 AI 回复正文，信噪比高；工具输出留待需要时再加）。

## 架构

### #2 正则识别文件卡片（前端）

三个纯函数（`preview.ts`，可单测）+ 渲染改点（`App.tsx`）：
- `extractFilePaths(text: string): string[]` —— 正则从文本抓"像文件路径"的候选 token（含已知扩展名、可带 `/`/`\` 前缀），去掉尾随标点。宽松即可，真正的门是下一步的存在性校验。
- `matchWorkspaceFiles(candidates: string[], files: string[]): string[]` —— 把候选与工作区文件列表比对，返回**真实存在**的工作区相对路径（按完整相对路径或以 `/<候选>` 结尾匹配），去重、保序。
- 渲染：`App.tsx` 的 `blocks.map` 里，在每个 **assistant** 块（`BlockView`）之后，用 `matchWorkspaceFiles(extractFilePaths(block.text), files)` 得到路径，逐个渲染现有 `ArtifactCard`（`onOpen={openArtifact}`）。
- **移除**当前 exec 组里基于 `previewableArtifacts` 的 ArtifactCard 渲染；同时**撤销 v3 的去重过滤**——`ExecutionCard` 恢复显示全部 `g.tools`（Write/Edit 作为紧凑步骤行照常出现，卡片改由正文正则产生）。
- `files` 时效：一个回合结束时刷新 `files`（`listFiles().then(setFiles)`），确保本回合新写的文件能被匹配到（复用/临近现有 turn-end 逻辑）。

### #4 `preview` 模型工具（后端 + 引擎 + 前端捕获）

**工具**（`tools/preview/preview.go`）：实现 `tool.Tool`。
- `Name() = "open_preview"`（模型看到的工具名）；`Description()` 引导："在你生成了文档类产物或网站(H5)后，调用它把该文件在用户桌面预览面板中打开。参数 path 为工作区内相对路径。"
- `InputSchema`：`{ path: string }`（必填）。
- `IsConcurrencySafe() = true`（只读展示）。
- `Run`：把 `path` 相对 `tctx.WorkingDirectory` 解析并校验在工作区内（复用 `internal/toolpath` 边界校验）、存在；然后在 `out` 上发一个事件带结构化 `Data`（仿 TodoWrite）：`out <- tool.Event{Type: EventTypeProgress, ToolName: "open_preview", Message: "预览 "+rel, Data: previewData{Path: rel}}`；返回 `Result` 文本（如"已在桌面打开预览：<rel>"）。越界/不存在 → 返回错误 `Result`（模型看到"文件不在工作区/不存在"），不发事件。
- CLI 下该工具**不注册**（见下），所以 CLI 模型看不到、不会调用。

**引擎注入点**（`internal/engine/engine.go` + `build.go`）：
- 在 `engine.Options`（engine.go:26，运行期选项，非序列化）加 `ExtraTools []tool.Tool`。
- `build.go`：在 sub-agent 工具快照**之后**（与 Task/Remember 一致，仅主会话可用）`sessionTools = append(sessionTools, opts.ExtraTools...)`。
- 桌面 `internal/desktop/app.go`（engine.Build 调用处）传 `ExtraTools: []tool.Tool{preview.New()}`；CLI（`cmd/runcode`）不传 → nil → 不注册。

**前端捕获**（`App.tsx` 工具事件处理）：收到 `toolName === 'open_preview'` 且 `data.path` 的工具事件时，`openArtifact(toWorkspaceRel(data.path, cwd))`（开成标签、展示右栏）。它仍会作为一条工具步骤出现在执行卡里（正常），额外触发一次预览打开。

## 数据流

```
AI 回复正文 → extractFilePaths → matchWorkspaceFiles(与 files 比对) → 现有 ArtifactCard（正文下方）
回合结束 → 刷新 files（新写文件可被匹配）
模型调用 open_preview(path) → 工具校验 → 发 tool.Event{ToolName:"open_preview", Data:{path}} → 桌面前端捕获 → openArtifact
CLI：open_preview 工具不注册，模型无此能力
```

## 错误处理 / 安全

- `preview` 工具：越界/不存在 → 错误 Result，不发事件、不启动任何东西（工作区边界 fail-closed，复用 toolpath）。
- 正则卡片：只渲染工作区**真实存在**的文件（存在性校验挡住误报，如模型泛写的 `index.html`）；点开走现有 `ReadArtifact`/预览路径（本身有边界校验）。
- 前端捕获 preview 事件：`toWorkspaceRel` 归一后 `openArtifact`；不可预览类型 → 面板显示"暂不支持"（现有降级）。

## 测试计划

**前端（vitest）**：
- `extractFilePaths`：从含路径的正文抓出候选（`cat.html`、`src/app.py`、`./README.md`、`D:\ws\demo.md`）；忽略纯文字/无扩展名；去尾随标点（`见 report.md。` → `report.md`）。
- `matchWorkspaceFiles`：候选与 files 比对，命中真实文件（全路径或 basename 结尾）返回相对路径、去重保序；不存在的候选被丢弃。

**Go**：
- `open_preview` 工具 `Run`：工作区内存在文件 → 返回成功 Result 且 `out` 收到 `ToolName=="open_preview"`、`Data` 为 `previewData{Path: rel}`；越界/不存在 → 错误 Result 且无事件。
- 引擎：`opts.ExtraTools` 被 append 进 sessionTools（可加个小测试或靠现有 build 测试覆盖）。

**手动**：桌面让模型生成 `site.html` 并调用 `open_preview` → 右栏自动打开；正文提到的 `README.md` 出卡可点开；CLI 里 `open_preview` 不在工具列表。

## 落点（文件）

- 前端：`preview.ts`（`extractFilePaths`/`matchWorkspaceFiles` + 测试）、`App.tsx`（assistant 块后渲染正则卡、移除 exec 组工具事件卡 + 撤销去重过滤、turn-end 刷新 files、捕获 preview 事件）。
- 后端：`tools/preview/preview.go`（+ 测试）、`internal/engine/engine.go`（`Options.ExtraTools`）、`internal/engine/build.go`（append）、`internal/desktop/app.go`（传 ExtraTools）。

## 已定决策

- 卡片来源：**只用正则**（扫 AI 回复正文，存在性校验挡误报），不再用 Write/Edit 工具事件生卡；exec 卡恢复显示全部步骤。
- `open_preview` 工具：**仅桌面注册**（`engine.Options.ExtraTools`），CLI 不暴露；经工具事件 `Data` 通道通知前端。
- 本轮只扫 AI 回复正文（不扫工具输出）。
