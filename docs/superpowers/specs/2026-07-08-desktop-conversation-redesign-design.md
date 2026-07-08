# 桌面对话区重设计（"构建日志"方向）

日期：2026-07-08
状态：已批准设计（经浏览器 mockup 逐屏确认），待写实现计划

## 背景

用户反馈现有对话/预览界面"有点丑、布局乱"，并提出两个具体诉求：写完文件能自动打开预览、窗口按钮放到最顶一行。经多轮 mockup 确认，定下一套整体方向——把对话区做成**安静、精确的"构建日志"**：AI 产出的文件是一等公民、可一眼扫读并跳入；执行细节安静收拢；窗口 chrome 规整。

同时修掉一个已暴露的脆弱点：可拖拽预览宽度会把对话区挤没（该修复已单独落地，见"宽度兜底"）。

## 范围

**做**：
1. **顶部全宽标题栏**：最小化/缩放/关闭移到最上独立一行（最右），XRUN 标志移到标题栏左侧；标题栏为无边框窗口拖动区。对话状态栏（空闲/上下文/压缩/预览）下移成独立一行。
2. **文件去重成一张卡**：Write/Edit 产物只渲染成**一张文件卡**（不再"步骤行 + 产物卡 + 正文链接"重复）；Bash/Grep/Read 等非文件操作收拢进紧凑的"执行过程"折叠区。
3. **类型色带（签名元素）**：每张文件卡左侧一条按类型编码的色带（HTML 琥珀、Markdown 蓝、代码 绿、其它 slate），并延续到预览标签的左缘与预览头的类型徽章。
4. **VS Code 风 SVG 类型图标**：`icons.tsx` 新增文件类型图标（无 emoji），图标不带底色块、直接跟在等宽文件名前。
5. **等宽承载信号**：文件名、diff、路径、命令用等宽字体。
6. **扁平用户消息**：右对齐、极淡薰衣草底，无饱和气泡。
7. **写完自动预览**：一个回合结束时，自动把该回合**最后写的可预览文件**开成标签并展开右栏；带一个"写完自动预览"开关（默认开，localStorage 持久化）。
8. **预览头去冗余**：文件名只在标签栏显示；预览头保留类型徽章 + 操作按钮（刷新/外部打开/复制路径/关闭）。
9. **宽度兜底**（已落地）：`clampPreviewWidth` + 面板 `maxWidth:60%`，对话区永不被挤没。

**不做（YAGNI / 越界）**：
- 集成 VS Code / code-server / Monaco（会毁掉轻量、与用户真实编辑器重复）——只借"观感"。
- 全应用换肤（设置/技能/权限等页保持现状）；预览内编辑；改动模型输出的正文。

## 设计令牌

复用应用**现有主题令牌**（`bg-surface`/`text-ink`/`text-muted`/`text-faint`/`border-line2`/`bg-inset`/`primary` 等），**新增**：
- **类型强调色**（供色带/徽章/图标，纯 JS 映射，按需 inline style）：`html #E39A3B`(琥珀)、`markdown #4C82F7`(蓝)、`code #2FAE6A`(绿)、`svg/image #E0679B`(玫红，避开主色紫)、`text/其它 #8A94A6`(slate)。对应浅底（徽章/图标软背景）取各色约 10% 透明度。
- **等宽字体族**沿用现有 mono 定义；确保文件名/diff/命令走 mono。
- **用户消息底**：极淡薰衣草（复用/新增一个 `bg-lav` ≈ `#F4F3FF`）。

不整体替换调色板——只在对话流/预览/标题栏这三块引入上述新增。

## 组件与结构变化

### A. 顶部标题栏（App.tsx + WindowControls）
- 在应用根（侧栏 + 内容的 flex 行之上）新增一条全宽 `titlebar`（约 38px）：左 = XRUN 标志（从侧栏顶部移来），中 = 拖动区（`--wails-draggable:drag` / 现有 `DRAG` 样式），右 = `WindowControls`（最小化/缩放/关闭；关闭 hover 变红）。
- 现有对话 `<header>`（52px）**移除窗口按钮**，改为纯"状态栏"（空闲/运行中 + ContextMeter + 压缩 + 预览 入口），高度可收到约 44px。
- 侧栏顶部去掉 XRUN 标志，直接从"新建对话"开始。

### B. 文件类型 → 图标/色带映射（preview.ts，纯函数，可单测）
- `kindAccent(kind: PreviewKind): string` —— kind → 强调色 hex（上表）。
- `kindIcon(kind)` 扩展为返回**新的文件类型图标名**（下）。
- 新增图标名与 SVG（`icons.tsx`）：`file-html`（`</>`）、`file-md`（Markdown M▼ 标记）、`file-code`（`{ }`）、`file-image`（图片）、`file-text`（文本行）。工具栏图标 `refresh`/`external-link`/`copy` 已有（v2）。

### C. 对话流渲染重构（App.tsx + 新组件）
一个 bot 回合按"动作"渲染，而非"步骤 + 卡"两套：
- **文件动作（Write/Edit，完成态）**→ `ArtifactCard` 改造为**类型色带卡**：左色带（`kindAccent`）+ 类型 SVG 图标（`kindIcon`）+ 等宽文件名 + 副标题"`类型` · `+add −del`" + hover 显 "打开 →"；已自动预览的加一枚小"已预览"标；整卡点击 = 面板内预览；右侧"打开方式"下拉（四项，v2 已有）。**去掉 v2 里与执行步骤行重复的渲染**。
- **动作 eyebrow**：卡上方一行极小的追踪字距标签"写入 / 运行 / 读取"（结构即信息）。
- **非文件操作（Bash/Grep/Read…）**→ 收拢进一行安静的等宽"执行过程 · N 步"折叠（沿用/收紧现有 ExecutionCard 的紧凑行；点开看详情）。
- **用户消息**：右对齐、极淡薰衣草底、无气泡（替换现有带 × 的胶囊样式）。

### D. 预览区（preview-panel.tsx）
- **类型色带延续**：`PreviewTabs` 的当前标签左缘 2px 用 `kindAccent(kind)`；`PreviewPanel` 头部类型徽章用同色。
- **去冗余**：文件名只在标签栏显示；`PreviewPanel` 头部不再重复文件名，保留类型徽章 + 图标 + 工具按钮。

### E. 写完自动预览（App.tsx + 纯函数）
- `newestPreviewableArtifact(tools): string | null` —— 从一个回合的工具事件里挑**最后一个**完成态、可预览的 Write/Edit 目标（工作区相对路径），无则 null。纯函数，可单测。
- 回合结束（现有 TurnEnd 事件处理处）：若"写完自动预览"开关开且该回合有可预览产物，`openTab(newest)` 并展开右栏。
- 开关"写完自动预览"：默认开，存 localStorage（键 `preview.autoOpen`）。放在右栏文件浏览器头部（无标签时可见）一个小 toggle；有标签时预览可见即知，不必再显开关。

## 数据流

```
回合结束(TurnEnd) → 收集该回合可预览 Write/Edit 产物
   若 autoOpen 开 且 有产物 → newestPreviewableArtifact → openTab(最后一个) → 展开右栏
AI 写/改文件 → 渲染为类型色带文件卡（图标/色带按 kind）
点卡 / 卡上"打开方式>预览" → openTab
类型色带延续到 预览标签左缘 + 预览头徽章
窗口按钮 → 顶部标题栏最右（min/max/close）
```

## 错误处理 / 边界

- 无可预览产物的回合 → 不自动打开（也不报错）。
- 关掉"写完自动预览" → 只手动点开。
- 预览宽度：`clampPreviewWidth`（启动夹取到窗口、越界回落默认 560）+ 面板 `maxWidth:60%`，对话区永远 ≥40%，坏值下次启动自愈。（已落地。）
- 未知/二进制类型 → slate 色带 + 通用"文件"图标，卡仍在，"预览"置灰（沿用 v2）。

## 测试计划

**前端（vitest，纯函数）**：
- `kindAccent`：各 kind → 正确 hex；未知 → slate。
- `newestPreviewableArtifact`：多产物取最后一个可预览的；混入不可预览/非完成态被跳过；空 → null。
- `clampPreviewWidth`：已覆盖（越界/NaN/过小回落，窗口夹取）。
- `kindIcon`：各 kind → 对应新图标名。

**组件 / 构建**：`npm run build` + `wails build` 出包；组件改造后 vitest 全绿。

**手动**：写 md/html/py 各一 → 每个一张色带卡（颜色对）、执行步骤折叠、写完右栏自动开最新文件（关开关后不自动开）、预览标签/头颜色延续、窗口按钮在最上一行且能最小化/缩放/关闭、拖拽缩放不再挤没对话、窄窗可用。

## 落点（文件）

- 前端：
  - `App.tsx`：顶部标题栏行 + 状态栏下移 + 对话流按动作渲染（文件卡 / 折叠步骤 / 扁平用户消息）+ 回合结束自动预览接线 + autoOpen 开关状态。
  - `artifact-card.tsx`：改造为类型色带卡（色带 + eyebrow + SVG 图标 + 等宽名 + hover 打开）。
  - `preview-panel.tsx`：标签/头部类型色带延续 + 预览头去文件名。
  - `preview.ts`：`kindAccent` + `newestPreviewableArtifact` + `kindIcon` 扩展（+ 已有 `clampPreviewWidth`）。
  - `icons.tsx`：新增 `file-html`/`file-md`/`file-code`/`file-image`/`file-text` SVG。
  - `preview.test.ts`：`kindAccent`/`newestPreviewableArtifact`/`kindIcon` 测试。
- 侧栏 XRUN 标志迁移：`App.tsx`（及可能的 `pages.tsx`/`Sidebar`）。

## 已定决策

- 方向：安静精确的"构建日志"。
- 窗口按钮 → 顶部全宽标题栏最右；XRUN → 标题栏左（不留在侧栏）。
- 文件卡签名 = 类型色带（HTML 琥珀 / MD 蓝 / 代码 绿），延续到预览。
- 图标全 SVG（参考 VS Code），无 emoji。
- 用户消息扁平、无气泡。
- 自动预览：回合结束开最新一个，带默认开的开关。
- 不集成 VS Code / Monaco；不整体换肤；不做预览内编辑。
