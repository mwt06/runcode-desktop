# 桌面产物预览（子项目 A：预览面板 + 浏览器原生类型）

日期：2026-07-08
状态：已批准设计，待实现

## 背景与目标

桌面版（Wails + React）需要让用户预览 AI 生成的产物。完整愿景包含 Markdown / HTML(H5) / 图片 / 代码 / 文本，以及 Office 文档、跑起来的前端等——这些难度差异很大，拆成三个子项目独立推进：

- **A（本 spec）**：预览面板 + 浏览器原生类型（Markdown / HTML(H5) / 图片 / SVG / 代码 / 文本）。含"预览面板 + 触发机制 + 本地静态服务器"这套地基。
- B（后续）：Office 文档（Word/PPT）。
- C（后续）：跑起来的前端（dev server 嵌入）。A 的静态服务器为 C 打基础。

本 spec 只覆盖 A。

### 现状（已具备）

- 前端已有 `Markdown` 组件（`markdown.tsx`：react-markdown + remark-gfm + rehype-highlight），代码高亮可复用。
- `ToolDetail` 已能渲染图片（data URI）和代码/文本（`<pre>`）。
- 视图为侧栏驱动；工具事件流入前端，`Write`/`Edit` 事件带文件路径。
- 后端有 `ListFiles()`；无"读文件供预览"绑定，无静态服务器，无 dev-server 基础设施。

## 范围

**做（v1）**：Markdown、HTML/H5、图片、SVG、代码、纯文本的预览；右侧可拖宽/折叠的预览面板；从 `Write`/`Edit` 产物自动识别可预览项并在工具卡上给"预览"入口；面板内一个极简文件浏览器兜底（点任意工作区文件预览）；本地静态服务器（供 HTML 及其外部资源、图片加载）。

**不做（YAGNI，后续）**：Word/PPT（B）、跑 dev server 的前端（C）、多产物 tab、在预览里编辑/回写、diff 对比、非文本二进制查看器。

## 架构

三个组件，各自单一职责、接口清晰、可独立测试：

### 组件 1：本地静态服务器（后端）

**做什么**：只读 serve 当前工作区目录，让 HTML 能解析其引用的 CSS/JS/图片，并按正确 content-type 提供图片等。

**接口 / 生命周期**：
- 新增 `internal/desktop/preview_server.go`，类型 `previewServer`：`start(workspace string) (baseURL string, err error)` / `stop()`。
- 监听 `127.0.0.1:0`（OS 分配端口，仅回环），`http.Server` + `http.FileServer(http.Dir(workspace))`。
- 只接受 `GET`/`HEAD`（其它方法 405）。
- 在 `App.buildAndSetLocked` 里随会话启动（工作区已知），在 `closeLocked` 里停掉；切换工作区即停旧起新。
- baseURL 形如 `http://127.0.0.1:<port>/`，放进 `SessionInfo.PreviewBaseURL`（会话开始即交付给前端），另提供 `App.PreviewBaseURL() string` 便于查询。

**依赖**：仅标准库 `net/http` / `net`。

**安全**（详见"安全"节）：仅回环、只读、限定工作区内、防 `..` 与 symlink 逃逸、CORS 允许应用源。

### 组件 2：读产物内容绑定（后端）

**做什么**：给前端取文本类产物内容（md/代码/文本/svg 源码），避免跨源 fetch（CORS）并加大小上限。

**接口**：`App.ReadArtifact(relPath string) (string, error)`。
- 相对工作区解析，经 `internal/toolpath` 同款校验限定在工作区内（复用现有路径边界，防越界/symlink）。
- 大小上限（如 2 MiB）；超限返回错误（前端提示"文件过大，用系统程序打开"）。
- 二进制/非 UTF-8 探测：返回错误或标记（前端转"不支持预览"）。

**依赖**：`internal/toolpath`。

### 组件 3：前端预览模块（`preview.tsx`，新建）

**为什么新建文件**：`App.tsx` 已很大；预览逻辑自成一块，放新模块保持高内聚，`App.tsx` 只接一个右栏挂载点与少量状态。

**导出**：
- `classifyPreview(path: string): PreviewKind` —— 纯函数，按扩展名分类为 `markdown | image | svg | html | code | text | unsupported`（`code` 附带高亮语言）。**可单测**。
- `PreviewPanel({ artifact, baseURL, onClose, onRefresh })` —— 按 kind 渲染（见"类型与渲染"）。头部：文件名、类型、刷新、关闭、"用系统程序打开"。
- `FileBrowser({ files, onPick })` —— 极简：从 `ListFiles()` 的路径构建**浅树/按目录分组的只读列表**，可预览文件可点击，其余灰显 + "外部打开"。

**App.tsx 集成**：主区改为水平 flex —— 对话（flex）+ 可拖分隔条 + 预览面板（打开时）。状态：`preview`（当前产物 { relPath, kind }）、`previewOpen`、`previewWidth`。`Write`/`Edit` 若 `classifyPreview` 可预览，则在该工具卡加"预览"按钮 → 设 `preview` 并打开右栏。窄窗口时面板全屏临时盖住。

## 数据流

```
开会话 → 后端起静态服务器(工作区) → SessionInfo.PreviewBaseURL 交付前端
AI 写文件 → 工具事件(带 path) → classifyPreview 可预览则给卡片"预览"
点"预览" / 文件浏览器选文件 → 右栏 PreviewPanel 打开 → 按 kind:
   html/htm  → 沙箱 <iframe src={baseURL + relPath}>       (资源经服务器解析)
   image/svg → <img src={baseURL + relPath}>               (svg 也可内联)
   markdown  → ReadArtifact(relPath) → <Markdown>
   code/text → ReadArtifact(relPath) → 高亮/<pre>
刷新 → 重取(AI 可能又改了文件)；iframe/img 加 cache-buster 查询参数
切换/关会话 → 服务器停/重起，PreviewBaseURL 更新，面板重置
```

## 安全

- **静态服务器**：绑 `127.0.0.1` 仅回环；只读（GET/HEAD）；`http.Dir` 天然挡 `..` 越界；额外包一层 handler，对每个请求把目标路径 `EvalSymlinks` 后校验仍在工作区内，**防 symlink 逃逸到工作区外**。仅本机进程可达。
- **HTML 沙箱 iframe**：`sandbox="allow-scripts allow-forms allow-popups allow-modals"`，**不给 `allow-same-origin`**。H5 的 JS 能运行，但内容处于 localhost 源、又是 opaque sandbox 源，**隔离于 Wails 应用**（`wails://`）——读不到应用存储、脚本不了父页。
- **WebView CSP**：应用 CSP 需放开 `frame-src`、`img-src`、`media-src` 到 `http://127.0.0.1:*`（端口随机，需允许整个回环段）；`ReadArtifact` 走 Wails 绑定不受 CORS 影响，故 `connect-src` 无需额外放开。静态服务器对应用源加 `Access-Control-Allow-Origin`（防将来直接 fetch 时受阻，可选）。
- `ReadArtifact` 复用 `internal/toolpath` 的工作区边界校验，杜绝越界读。

## 类型与渲染

| kind | 扩展名 | 渲染 |
|---|---|---|
| markdown | `.md .markdown` | `ReadArtifact` → 复用 `Markdown` 组件 |
| image | `.png .jpg .jpeg .gif .webp .bmp .ico` | `<img src=服务器URL>` |
| svg | `.svg` | `<img>`（或内联，供缩放） |
| html | `.html .htm` | 沙箱 `<iframe src=服务器URL>` |
| code | `.js .ts .tsx .jsx .py .go .rs .java .c .cpp .css .scss .json .yaml .yml .toml .sh .sql …` | `ReadArtifact` → 语法高亮 |
| text | `.txt .log .csv .env` 及其它 UTF-8 文本 | `ReadArtifact` → `<pre>` |
| unsupported | 二进制/未知 | "暂不支持预览" + "用系统程序打开" |

## 错误处理

- 不支持类型 → 面板显示"暂不支持预览"，提供"用系统程序打开"（复用/新增 `OpenExternal(relPath)` 绑定，调用 OS 默认程序）。
- 文件不存在/读失败 → 面板内错误提示 + 重试。
- 静态服务器起不来 → md/代码/文本走 `ReadArtifact` 仍可用；html/图片显示"预览服务不可用"降级。
- 超大文件 → `ReadArtifact` 拒绝 + 提示外部打开；图片/HTML 由浏览器自身处理（可加尺寸提示）。
- 会话未开（无工作区）→ 预览入口禁用。

## 测试计划

**后端（Go，TDD）**：
- `previewServer`：serve 工作区内文件返回内容与正确 content-type；`..` 越界 404/403；symlink 逃逸到工作区外被拒；非 GET/HEAD 405；`start` 返回可用 baseURL、`stop` 关闭；换工作区重起。
- `ReadArtifact`：工作区内文本正常返回；越界/symlink 逃逸被拒；超限报错；二进制探测。

**前端（vitest）**：
- `classifyPreview`：各扩展名 → 正确 kind（含大小写、多段扩展名、未知→unsupported、code 语言映射）。

**手动/构建**：`wails build` 出包，实测 md / html(带外部 CSS/JS/图片) / 图片 / 代码 在右栏预览、拖宽折叠、刷新、窄窗全屏。

## 落点（文件）

- 后端：`internal/desktop/preview_server.go`（静态服务器）、`internal/desktop/preview.go`（`ReadArtifact`/`OpenExternal`/`PreviewBaseURL` 绑定）、`internal/desktop/types.go`（`SessionInfo.PreviewBaseURL`）、`app.go`（生命周期接线）。
- 前端：`cmd/runcode-desktop/frontend/src/preview.tsx`（`classifyPreview` + `PreviewPanel` + `FileBrowser`）、`preview.test.ts`（分类测试）、`bridge.ts`（`readArtifact`/`openExternal` 绑定 + `PreviewBaseURL` 类型）、`App.tsx`（右栏布局挂载 + 工具卡"预览"按钮 + 状态）。
- 配置：Wails CSP 放开 `127.0.0.1:*`（`wails.json` 或 index.html meta / 资产中间件，取实现时确认 Wails 的 CSP 配置点）。

## 已定决策（本轮）

- 触发：产物自动识别（Write/Edit）**+** 文件浏览器兜底。
- 布局：右侧分栏（可拖宽/折叠，窄窗口全屏临时盖住）。
- HTML：完整（含外部资源）→ 引入本地静态服务器。
- 文件浏览器 v1：极简只读浅树/分组列表。
- 类型清单：如上表（md/html/img/svg/code/text）。

## 待实现时确认的小问题

- Wails 的 CSP 具体配置点（wails.json 有无 CSP 字段，或需在 index.html meta / 资产中间件设置）。
- `OpenExternal` 的跨平台实现（Windows `rundll32 url.dll,FileProtocolHandler` / `start`；Wails runtime 是否已提供 `BrowserOpenURL` 可复用于 `file://` 或本地 URL）。
