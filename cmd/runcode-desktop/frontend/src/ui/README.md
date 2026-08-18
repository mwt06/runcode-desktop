# `ui/` —— 公共组件与设计 token

这一层放**与业务无关**的通用件。判据是：它认不认识"会话""技能""MCP"这些概念——认识就不属于这里，该去 `chat/` `preview/` `pages/`。依赖只朝下：`ui/` 只能引 `@/core`，不能引 `@/chat` `@/session` `@/pages`（`core` 自己只引 `@/assets`，`hooks` 谁都不引）。

**改了这里请同步本文。** 文档过时比没有文档更糟——它会让人以为已经有共用件而重复造，或者以为没有而手写一遍。

---

## 一、设计 token（`../styles.css` 的 `@theme`）

**颜色、圆角、阴影、字号一律用 token，不要写 `#hex`、`rgba()` 或 `text-[12.5px]` 这类字面值。** 这不是洁癖：本仓出过 warning 并存两套颜色、同一个按钮浮起阴影并存 0.32/0.30 两个透明度、同一个圆角同时写成 `rounded-lg` 和 `rounded-[8px]` 的事故，全都是从"就这一处，先写死"开始的。

### 颜色

| 用途 | token | 值 |
| --- | --- | --- |
| 页面底色 / 卡片面 / 次级面 / 嵌入面 | `bg` `surface` `surface2` `inset` | `#eceef4` `#ffffff` `#f6f7fb` `#f3f4f8` |
| 浅分隔线 / 常规边框 | `line` `line2` | `#edeef4` `#e4e7ef` |
| 标题墨 / 正文墨 / 次要 / 更次要 | `ink` `ink2` `muted` `faint` | `#21242e` `#3f4653` `#646b7d` `#8a92a3` |
| 主色 / 主色文字 / 主色浅底 | `primary` `primaryink` `primarysoft` | `#5b6cf0` `#4856d6` `#eef0fe` |
| 用户消息气泡 | `userbg` | `#f4f3ff` |
| 成功 / 成功浅底 | `green` `greenbg` | `#1f9d63` `#e7f6ee` |
| 失败 / 失败浅底 | `red` `redbg` | `#e0564a` `#fdeceb` |
| 警告标记 / 警告文字 | `amber` `amberink` | `#e3a23c` `#9a6b12` |

两组容易用错的：

- **`ink` vs `ink2`**：标题、强调、需要压住页面的用 `ink`；连续正文（助手回复、工具标签、卡片说明）用 `ink2`，它更柔，长段落不会显得砸。
- **`amber` vs `amberink`**：`amber` 是**填充色**——圆点、进度条、边框；它在白底上对比度只有约 2.3:1，**小字用它不达标**。警告文案一律 `amberink`（约 5.5:1）。与 `primary`/`primaryink` 是同一种配对关系。

透明度用斜杠语法（`border-red/35`、`bg-amber/12`），不要手写等价的 `rgba()`——那样 token 改了它不跟。

**三类例外**，只有这三类可以出现字面色值：`core/…/classify.ts` 的文件类型色与 `icons.tsx` 的图标品牌色（那是**内容色**，跟随文件格式而非主题），以及 `main.tsx` 里 ErrorBoundary 的兜底样式（渲染崩溃时不能依赖任何类名系统，必须内联）。

### 圆角

`rounded-field`(9px) `rounded-btn`(10px) `rounded-card`(14px) `rounded-md`(6) `rounded-lg`(8) `rounded-xl`(12) `rounded-2xl`(16) `rounded-full`

前三个是按用途命名的：**field** 是一切输入类控件（`FIELD_CLS`、`INSET_BOX`、下拉、文本域），**btn** 是按钮与按钮状方块（图标格、浮层），**card** 是抬起的卡片/区块面。

`@theme` 把 Tailwind 的 rem 预设改锚成了 px（值不变）。原因：本应用是固定尺寸的桌面壳、字号全是 px，只有圆角锚在 px 上，`rounded-lg` 与手写的 `rounded-[8px]` 才**真正**等价——否则根字号一动它们就悄悄分叉。

### 阴影

| token | 用在哪 |
| --- | --- |
| `shadow-xs` | 卡片的轻微离地 |
| `shadow-card` | 常规卡片/浮层 |
| `shadow-focus` | 输入框聚焦光环（配合 `focus:border-primary`） |
| `shadow-lift` / `shadow-lift-danger` | 实色主按钮的浮起（发送、侧栏主操作 / 停止） |
| `shadow-modal` | 模态对话框 |

要新效果就去 `@theme` 加一档，不要在调用点写 `shadow-[…]`。

### 字号

**只用整数 px，没有半档**：`9 10 11 12 13 14 15 16 18 20 22 24 26`。主力是 **12**（次要信息、脚注）与 **13**（正文、控件文字）。

曾经并存 20 档、含六个半档共 141 处。0.5px 在本应用的 DPI 下渲染差异接近 0，**起不到层次作用**，只是每次随手挑的痕迹——一度出现过 `ask-card` 的标题（12.5px）比正文（13px）还小。要拉层次请跨整档。

---

## 二、反馈：`feedback.tsx`

**"出错了/要注意"只能从这里出**，不要在调用点手写红色类名。曾经散成六种形状十九处：带边框横幅、带图标横幅、三种字号的裸文字、对话气泡、行内小胶囊。

共用一个严重度轴 `Tone = 'neutral' | 'warning' | 'danger'`，它同时决定颜色**和字号**——调用点不要另挑字号。

### `SystemNote` —— 对话流里的系统事件条

```tsx
<SystemNote tone="danger" icon={<WarnTriangle />} selectable>{text}</SystemNote>
```

居中标签 + 向两侧延伸的横线。**刻意不是气泡**：气泡在助手列里，会被读成"模型说的话"，而这些是应用自己在说话（压缩发生了、重试了、回合失败了）。`error` / `warning` / `notice` / `compaction` / `retry` 五种全走它。

| prop | 说明 |
| --- | --- |
| `tone` | 严重度，默认 `neutral` |
| `icon` | 可选前置图标，警告/错误用 `<WarnTriangle />` |
| `sub` | 可选第二行，居中在横线下（压缩的 token 计数用） |
| `selectable` | 装饰性的省略；**凡是带错误原因的必须传**——用户要能复制去报 bug |
| `title` | 原生 tooltip |

标签会换行而不是 `nowrap`：引擎错误文本长度不定，`nowrap` 会撑破面板；短标签够不到最大宽度，不受影响。

### `Banner` —— 弹窗/表单里的块状提示

```tsx
<Banner tone="warning" icon={<Icon name="shield" size={16} />} title="超出本项目范围">
  正文
</Banner>
```

左侧图标栏 + 粗体标题 + 正文。**正文颜色跟着标题走**：有标题时标题担纲严重度、正文用 `ink`（长文舒服）；无标题时正文**就是**那条消息，继承 tone 色而不是变灰。

> 权限弹窗里的 sampling 提示（`bg-primarysoft`）结构相似但**没有**套这个——它是信息卡不是警示，正文该是 ink，套进来会和上面这条规则打架。语义不同就别硬套。

### `InlineError` —— 页面级失败

```tsx
{error && <InlineError>{error}</InlineError>}                    // 带边框，要用户处理的失败
{error && <InlineError variant="text">{error}</InlineError>}     // 安静版，贴着触发它的控件
```

两个变体都保留 `pre-wrap` + `break-words`：后端错误带换行和长路径，旧的裸文字调用点会把它们截掉。传空内容时渲染 `null`，可以安全地写 `{error && …}` 或直接 `<InlineError>{error}</InlineError>`。

---

## 三、布局：`layout.tsx`

### `PageShell` —— 管理页骨架

```tsx
<PageShell title="设置" hint="模型与权限模式即时生效…" width={640}>…</PageShell>
<PageShell title="MCP 服务器" hint={<>…含 <code>标记</code> 的富文本…</>} action={<button/>}>…</PageShell>
```

滚动列 + 居中版心 + 标题/副标题 + 可选右上动作。`width` 是阅读版心（默认 720；设置页 640，因为它是表单不是文档）。mcp / memory / settings 三页用它——此前各写一遍，已经在内边距和标题字号上漂开过。

`width` 走内联 style 而非 `max-w-[…]` 类：Tailwind 靠扫源码里的**字面**类名生成 CSS，运行时拼的类名根本不会被产出。

> `plugins` 与 `permissions` **不套**这层：它们有各自的固定页头（独立滚动体）和更宽的文档版心，抹平差异要加一堆开关，那样 shell 就变成配置容器了。

### `Placeholder` —— 空态与加载态

```tsx
<Placeholder>加载中…</Placeholder>              {/* pad="md"：卡片或区块内 */}
<Placeholder pad="lg">还没有技能…</Placeholder>  {/* 独占一个标签页体时 */}
```

### `InsetRow` / `INSET_BOX` —— 内嵌条

设置行、状态条、"已登录为…"。`INSET_BOX` 单独导出是因为有些调用点只要那个表面、不要 space-between 布局（纯说明文字）。

---

## 四、表单：`fields.tsx` · `toggle.tsx` · `model-picker.tsx`

| 导出 | 用途 |
| --- | --- |
| `FIELD_CLS` | **唯一的输入框外观**（surface2 底、line2 边、field 圆角、14px、primary 聚焦环）。`<input>` `<textarea>` 直接挂它 |
| `LABEL_CLS` | 配套的字段容器：竖排的次要说明 + 控件 |
| `SelectField` | 包装过的原生 `<select>`，与 `FIELD_CLS` 像素对齐 |
| `Toggle` | iOS 风格开关。点击**不冒泡**，可以安全放进整行可点的卡片 |
| `ModelSelect` / `ModelPickerPopover` | 共享的模型选择器：对话内切换、设置页会话/判定模型、子代理模型选同一个 |
| `toModelOptions(platform, custom)` | 平台模型 + 自定义连接合并成一份候选（**纯函数，有单测**） |

`ModelOption.id` 是选中后提交的值；`modelId` 只在两者不同时（自定义连接）用来标记"当前选中"。

---

## 五、按钮与浮层

| 导出 | 用途 |
| --- | --- |
| `BTN`（`tokens.ts`） | 标准按钮底样式 |
| `BTN_PRIMARY` / `BTN_DANGER` | 叠在 `BTN` 上的变体。**带 `!important`**——同优先级的 utility 不保证按类名顺序取胜，没加 `!` 的 `bg-primary` 曾输给 `bg-surface`，主按钮渲染成白底白字 |
| `GhostBtn` | 无边框工具条按钮（输入区工具条、卡片动作），未 hover 时不带任何 chrome |
| `Popover` | 共享的点击外部即关下拉。全屏透明遮罩接管点击（并阻止事件穿透到底下的元素），面板绝对定位到最近的 `relative` 祖先=触发器容器。`variant`：`menu` 紧凑列表 / `panel` 搜索选择器 |
| `ConfirmDialog` | 应用内确认框。**替代 `window.confirm()`**——原生框会带上难看的 "wails.localhost 显示" 头 |

用 `Popover` 时记得触发器的包裹元素要有 `relative`。

---

## 六、展示件

| 导出 | 用途 |
| --- | --- |
| `Markdown` | 助手文本渲染（GFM + 代码高亮），块代码保留 highlight.js 配色、行内代码是紧凑 chip |
| `Icon` / `Logo` / `toolIcon` / `TOOL_ICON` | 内联 SVG（`stroke=currentColor`），无图标字体、无网络请求。`TOOL_ICON` 把工具/内置子代理名映射到图标，对话工具行与插件管理页共用，**同一能力在哪都是同一个字形** |
| `CheckMark` / `WarnTriangle` / `Spinner`（`glyphs.tsx`） | 画出来的小状态图形（完成/警告/进行中）。与 `Icon` 分开是因为这些是**绘制**的，不是查表来的 |
| `DiffStat` / `SourceBadge` / `sourceLabel`（`badges.tsx`） | `+N −N` 变更量；技能/子代理/工具的来源标（内置/用户/项目） |
| `CollapsibleGroup` | 超过 `threshold`（默认 2）条就折叠成一行摘要，免得一长串编辑卡淹没对话。`expanded` 初始为 `null`=跟随默认，所以回合中途数量涨过阈值也会自动折叠；用户手动切过之后以用户的选择为准 |

---

## 七、`keys.ts` —— 输入法安全

```ts
if (isComposingKey(e)) return   // 放在所有 Enter/方向键快捷键判断的最前面
```

**凡是把 Enter 或方向键当快捷键的地方，都必须先过它。** 中日韩输入法用 Enter 上屏候选词、用方向键翻候选页——组字期间这些键跟"发送消息""选中候选文件"毫无关系，抢了就会出现"打字打一半消息就发出去了"。

判据有两条且缺一不可（浏览器引擎对"上屏那一下 Enter"的事件顺序不一致），细节见源码注释。纯函数，有单测。

---

## 八、`tokens.ts` 的 `DRAG` / `NO_DRAG`

窗口是无边框的（Frameless），所以要显式标出可拖拽区域，并自行提供窗口控制按钮。`DRAG` 标记拖拽区，`NO_DRAG` 让其中的交互元素退出拖拽——**忘了给按钮加 `NO_DRAG`，它就点不动只能拖窗口。**

---

## 九、看效果

`dev/` 下的预览页，不进正常流程（`wails dev` 后在地址后加参数）：

```
?preview=ui         ← 公共组件画廊：本文每一项的实物 + 全部 token 色板
?preview=tools         工具卡各状态
?preview=thinking      思考面板
```

**`?preview=ui` 也是视觉回归的检查台。** token 化、字号并档、圆角合并这类改动，`tsc` / `eslint` / `vitest` 一个都证明不了观感——改完开这一页扫一眼最快。

画廊里色板/圆角/阴影都经 `var(--color-…)` 这类 CSS 变量取值，而不是 `` `bg-${name}` `` 拼类名：**Tailwind 只扫源码里的字面类名，运行时拼出来的那个永远不会被生成**（`PageShell` 的 `width` 走内联 style 也是同一个原因）。副作用是它顺带验证了 token 真的存在——变量名拼错，那一格就是空的。
