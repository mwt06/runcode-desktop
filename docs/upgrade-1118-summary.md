# feat/upgrade-1118 升级摘要

对比基线：原始 `main` / `1a7d819`。

## 生图

- 增加生图模型目录、租户级生图设置和自动启用逻辑。
- 支持固定尺寸、多尺寸和不固定尺寸模型。
- 增加 `image_generate` 工具调用、唯一一次 HTTP 提交边界、权限审批和用量账本。
- 增加生成结果存储、恢复、重放、fork、编辑保存和产物引用校验。
- 增加生成中扫光、完成揭示、大图查看和行下预览。
- 生图工具独立为 `image` 分组，不再显示通用 ExecutionCard 折叠行与 ToolDetail。
- 保留完整图片标注链路，包括编辑画板、操作栈、导出、`SaveImageGenerationEdit` 和查看器标注状态。

## 本机用量

- 增加 SQLite 本机用量存储和 SQLite WAL 查询。
- 记录 LLM、视觉、生图、搜索等调用及成功、失败、取消、未知等状态。
- 增加采集覆盖诊断、崩溃恢复、租户/身份隔离和采集关闭区间。
- 增加用量概览、趋势、分页请求日志、维度筛选和缓存命中估算。

## 内联预览

- 支持 Mermaid、SVG、HTML 和路径文件预览。
- 流式回复只预览已闭合围栏。
- Mermaid 串行渲染，失败回退，过期异步结果丢弃。
- SVG/CSS/HTML 白名单清洗，HTML CSP、sandbox 和消息通道校验。
- 支持源码/预览直接切换、复制、下载、全屏、HTML 缩放和高度治理。
- 预览不再显示常驻标题框，操作按钮仅在悬浮或键盘聚焦时显示。
- 修复输入框打字导致预览和 Markdown 子树重挂载的问题。

## 会话与稳定性

- 预览引用绑定来源会话和 generation，失效后及时释放。
- 后台会话状态不覆盖当前焦点。
- 自动预览排除生成图片，并避免与正文内联预览重复。
- 退出时等待会话、录音和用量存储收尾。
- 生成图舞台在生成中到完成期间锁定几何，避免对话栏上移。
- 相对本地 Markdown 图片不再渲染破图占位。

## 输入与界面

- 输入框迁移到 Lexical。
- 增加设置页导航、生图设置和媒体分类。
- 生图工具使用图片加号图标。
- 增加 Mermaid/SVG 的实际图宽按钮对齐和悬浮控制。

## 精简

- 生图从通用 `exec` 分组拆为独立 `image` 分组。
- 统一 `IMAGE_GENERATION_TOOL` 和 `isGeneratedImageRel`。
- 删除废弃 `htmlInlineScale`、`GeneratedImageViewerHost.onSaved` 和失效测试断言。
- 抽出 `generated-image-size.ts` 和 `inline-preview-html.tsx`，降低组件职责。
- 自动生成的协议文件仍由 `tools/protogen` 维护。

## 验证

- 前端 88 个测试文件、966 个测试通过。
- 前端 TypeScript、ESLint、Vite 生产构建通过。
- Go 全量测试通过。
- `tools/protogen --check` 通过。
