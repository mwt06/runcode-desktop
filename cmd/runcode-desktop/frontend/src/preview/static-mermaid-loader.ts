// 全应用共用一个 Mermaid 渲染器：Mermaid 的模块级状态只能串行使用，多个实例各自
// 排队等于没排。模块按需动态导入，打成独立分块随客户端分发，首次遇到 Mermaid 才加载；
// 分块在旧 WebKit 上加载失败时只让该图退回源码，不影响主界面。
import { createMermaidRenderer, type MermaidApi } from './static-mermaid'

export const renderMermaid = createMermaidRenderer(
  () => import('mermaid').then((m): MermaidApi => m.default),
)
