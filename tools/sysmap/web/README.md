# docs 里那两个 HTML 是怎么来的

`docs/system-overview.html` 与 `docs/system-trees.html` 是**生成物**，为了方便双击就能看而提交进库。它们各自是一个自包含单文件：没有外链、没有 CDN、离线可用，浅深主题都做了，右上角有主题切换。

数据源头只有一个——`tools/sysmap` 的类型扫描：

```bash
go run ./tools/sysmap > sysmap.json      # 需 go.work 联动 ../agentloop
```

两个页面里的每个数字（包数、行数、接口与实现、方法与引用数）都从这份 JSON 来，没有手写。

## 重新生成

页面模板与装配脚本没有提交——它们用到 Node 侧的两个外部工具，为一个文档链路把整套 Node 构建正式化不值得。当前这两份 HTML 是这样做出来的，需要重做时按此复现：

1. **扫描**：`go run ./tools/sysmap > sysmap.json`
2. **`system-overview.html`**：把 JSON 裁剪后内联进页面模板；页面里 4 张 mermaid 图用 `@mermaid-js/mermaid-cli`（`mmdc`）预渲染成 SVG 再内联——本地没有 mermaid 运行时，不预渲染只会显示成源码。
   > 坑：**不要对 mmdc 产出的 SVG 做浮点精度压缩**。mermaid 用紧凑路径记法（`v-17l.001-.45`），四舍五入会写坏 `d` 属性，浏览器报 `<path> attribute d: Expected number`。压缩只省 5%，不值得。rough.js 的输出是显式 `M x y C …`，压掉 62% 且安全。
3. **`system-trees.html`**：五棵树由 `rough.js`（UMD，28 KB，内联）在浏览器里现画——手绘几何是运行期生成的，所以整页只有 60 KB，而同样五张图让 mermaid 的 `look: handDrawn` 预渲染要 1.5 MB。
   > mermaid 的 handDrawn 试过，效果不够：只有连线弯，方框仍是直的，且 roughness 不可配。

## 校验口径

两个页面都在无头 Chromium 里跑过：无 console/page 报错、无失败请求（证明真离线）、正文不横向溢出、主题三态切换正常、交互（折叠/搜索/筛选/缩放）逐个触发无异常，且渲染出的节点数与扫描数据逐项对齐。

改动页面后请照同样口径过一遍——尤其**横向溢出**：`.sm-card-nums` 那类 `white-space: nowrap` + `flex: none` 的元素，其 min-content 宽度会变成 grid 轨道的硬下限，把整页顶出横向滚动条（已修，见该处注释）。
