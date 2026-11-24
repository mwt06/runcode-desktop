// 对话正文内静态预览（CHAT-PREVIEW-001）的识别规则：哪些代码块可以变成图，哪些
// 必须保持源码。纯函数，不碰 React 与会话状态，Markdown 接入层只负责把解析器给出的
// 原文位置、语言与正文喂进来。
//
// 只认围栏代码块：不扫描正文里裸露的 <svg>/<html>，也不按内容猜无语言代码块的类型。

export type StaticPreviewKind = 'svg' | 'mermaid' | 'html'

// 设计默认预算（方案 7.2）。改动前先在目标机实测；超出一律退回源码，不截断渲染。
export const PREVIEW_LIMITS = {
  markupBytes: 256 * 1024, // HTML / SVG 单段源文本
  mermaidBytes: 32 * 1024,
  mermaidNodes: 200,
  mermaidEdges: 400,
  mermaidStatements: 600,
  perMessage: 6,
  /** iframe 内容量高上限（防百分比高度反馈）；内联可视高度见 htmlInlineMaxHeight。 */
  htmlMaxHeight: 2400,
  /** 对话内 HTML 预览外壳可视高度上限（px）；超出外层纵向滚动。 */
  htmlInlineMaxHeight: 560,
  /** 内联 HTML 移动页设计宽（CSS px）；100% 缩放 = 设计宽 1:1。 */
  htmlDesignWidth: 390,
  /** 脚本页先提供稳定的移动端视口，避免 flex 主区在首次测高前被压成 0。 */
  htmlScriptViewportHeight: 780,
  liveFrames: 4,
} as const

/** 用户缩放相对设计宽；显示为 xx%，有效 scale = percent/100。 */
export const HTML_ZOOM_MIN = 15
export const HTML_ZOOM_MAX = 250
export const HTML_ZOOM_STEP = 10

export function clampHtmlZoomPercent(n: number): number {
  if (!Number.isFinite(n)) return 100
  return Math.min(HTML_ZOOM_MAX, Math.max(HTML_ZOOM_MIN, Math.round(n)))
}

export function stepHtmlZoomPercent(current: number, direction: 1 | -1): number {
  return clampHtmlZoomPercent(current + direction * HTML_ZOOM_STEP)
}

/** 保持缩放前视区中心指向的内容位置；能放下时由容器 auto margin 居中。 */
export function htmlZoomScrollLeft(opts: {
  previousWidth: number; previousViewport: number; scrollLeft: number; width: number; viewport: number
}): number {
  const { previousWidth, previousViewport, scrollLeft, width, viewport } = opts
  if (!(width > 0) || !(viewport > 0) || !Number.isFinite(width + viewport)) return 0
  const center = previousWidth > previousViewport && previousViewport > 0 && Number.isFinite(previousWidth + scrollLeft)
    ? (scrollLeft + previousViewport / 2) / previousWidth
    : 0.5
  return Math.max(0, Math.min(width - viewport, center * width - viewport / 2))
}

const HTML_LANGS = new Set(['html', 'htm', 'h5'])
const SVG_LANGS = new Set(['svg', 'xml', 'svg+xml', 'image/svg+xml'])
const MERMAID_LANGS = new Set(['mermaid', 'mmd'])

export function utf8Bytes(text: string): number {
  return new TextEncoder().encode(text).length
}

// fenceClosed 判断 source[start, end) 这一段围栏代码块在原文里是否真有结束围栏。
// Markdown 解析器会把没写完的围栏一直延伸到容器末尾，照样产出代码节点，所以
// 「有代码节点」不等于「代码完整」。规则按 CommonMark：结束围栏与开头同字符、长度
// 不短于开头、其后只允许空白，且相对开头的缩进不超过 3 列。列表缩进与引用块的 `>`
// 前缀算作容器，比较前去掉。
export function fenceClosed(source: string, start: number, end: number): boolean {
  if (!(start >= 0 && end > start && end <= source.length)) return false
  const lines = source.slice(start, end).replace(/\r\n?/g, '\n').replace(/\n+$/, '').split('\n')
  if (lines.length < 2) return false
  const open = /^[ \t]*(`{3,}|~{3,})(.*)$/.exec(lines[0])
  if (!open) return false // 缩进式代码块没有围栏，也就没有语言，不参与预览
  const fence = open[1]
  if (fence[0] === '`' && open[2].includes('`')) return false
  const lineStart = source.lastIndexOf('\n', start - 1) + 1
  const baseIndent = indentWidth(source.slice(lineStart, start) + leadingSpace(lines[0]))
  const last = lines[lines.length - 1]
  const close = /^([ \t>]*?)(`{3,}|~{3,})[ \t]*$/.exec(last)
  if (!close) return false
  if (close[2][0] !== fence[0] || close[2].length < fence.length) return false
  return indentWidth(close[1]) - baseIndent <= 3
}

function leadingSpace(line: string): string {
  return /^[ \t]*/.exec(line)?.[0] ?? ''
}

// 容器前缀的视觉宽度：引用标记 `>`（连同其后一个可选空格）不计，列表标记与空白计列。
function indentWidth(prefix: string): number {
  let width = 0
  const stripped = prefix.replace(/>[ ]?/g, '')
  for (const ch of stripped) width += ch === '\t' ? 4 - (width % 4) : 1
  return width
}

export type CodeBlockInput = { lang?: string | null; meta?: string | null; value: string; closed: boolean }
export type CodeBlockDecision = { kind: StaticPreviewKind } | { kind: null; reason: string }

// classifyCodeBlock 决定一个围栏代码块是否进入静态预览。只做低成本的形状判断；
// 真正的语法与安全校验在各渲染器里，失败同样退回源码。
export function classifyCodeBlock({ lang, meta, value, closed }: CodeBlockInput): CodeBlockDecision {
  const language = (lang ?? '').trim().toLowerCase()
  const flags = new Set((meta ?? '').trim().toLowerCase().split(/\s+/).filter(Boolean))
  if (flags.has('source')) return { kind: null, reason: 'source' }
  if (!closed) return { kind: null, reason: 'unclosed' }
  const body = value.trim()
  if (!body) return { kind: null, reason: 'empty' }

  // 无语言：仅当正文已是完整 SVG / HTML 文档时才预览，绝不按内容猜代码语言。
  if (!language) {
    if (looksLikeSvgDocument(body)) {
      if (utf8Bytes(body) > PREVIEW_LIMITS.markupBytes) return { kind: null, reason: 'too-large' }
      return { kind: 'svg' }
    }
    if (looksLikeHtmlDocument(body)) {
      if (utf8Bytes(body) > PREVIEW_LIMITS.markupBytes) return { kind: null, reason: 'too-large' }
      return { kind: 'html' }
    }
    return { kind: null, reason: 'no-language' }
  }

  const isMermaid = MERMAID_LANGS.has(language)
  const isSvg = SVG_LANGS.has(language)
  const isHtml = HTML_LANGS.has(language)
  if (!isMermaid && !isSvg && !isHtml) return { kind: null, reason: 'language' }

  const limit = isMermaid ? PREVIEW_LIMITS.mermaidBytes : PREVIEW_LIMITS.markupBytes
  if (utf8Bytes(body) > limit) return { kind: null, reason: 'too-large' }
  if (isMermaid) return { kind: 'mermaid' }
  if (isSvg) return looksLikeSvgDocument(body) ? { kind: 'svg' } : { kind: null, reason: 'not-svg' }
  // html 围栏里若整段就是静态 SVG，按 SVG 走（避免不必要的 iframe）。
  if (looksLikeSvgDocument(body)) return { kind: 'svg' }
  // 完整网页、显式 preview，或片段内已含完整 <svg>…</svg>（常见「html 里画 SVG」）。
  if (looksLikeHtmlDocument(body) || flags.has('preview') || containsSvgDocument(body)) return { kind: 'html' }
  return { kind: null, reason: 'html-fragment' }
}

// 根元素是 <svg> 且以 </svg> 收尾；允许前置 XML 声明、注释与 doctype。
function looksLikeSvgDocument(body: string): boolean {
  const head = body.replace(/^(?:<\?xml[\s\S]*?\?>|<!--[\s\S]*?-->|<!DOCTYPE[^>]*>|\s+)*/i, '')
  return /^<svg[\s>]/i.test(head) && /<\/svg\s*>$/i.test(body)
}

// 完整网页：含 <html …> 起始与 </html> 结尾。讲解 <button> 写法的片段不在此列，
// 需要显式 `html preview` 才会预览。
function looksLikeHtmlDocument(body: string): boolean {
  return /<html[\s>]/i.test(body) && /<\/html\s*>$/i.test(body)
}

// 片段内是否嵌套完整 SVG 文档（允许前后有其它静态标签）。
function containsSvgDocument(body: string): boolean {
  return /<svg[\s>][\s\S]*<\/svg\s*>/i.test(body)
}

// contentDigest 是缓存键与去重用的同步摘要（cyrb53 + 长度），不是安全哈希。
export function contentDigest(text: string): string {
  let h1 = 0xdeadbeef
  let h2 = 0x41c6ce57
  for (let i = 0; i < text.length; i++) {
    const c = text.charCodeAt(i)
    h1 = Math.imul(h1 ^ c, 2654435761)
    h2 = Math.imul(h2 ^ c, 1597334677)
  }
  h1 = Math.imul(h1 ^ (h1 >>> 16), 2246822507) ^ Math.imul(h2 ^ (h2 >>> 13), 3266489909)
  h2 = Math.imul(h2 ^ (h2 >>> 16), 2246822507) ^ Math.imul(h1 ^ (h1 >>> 13), 3266489909)
  const hi = (h2 >>> 0).toString(16).padStart(8, '0')
  const lo = (h1 >>> 0).toString(16).padStart(8, '0')
  return `${hi}${lo}-${text.length.toString(16)}`
}

// RENDERER_VERSION 进入缓存键：清理规则或渲染配置变了，旧缓存自然失效。
export const RENDERER_VERSION = 1

export function previewCacheKey(parts: { sessionId: string; messageId: string; kind: StaticPreviewKind; digest: string }): string {
  return [RENDERER_VERSION, parts.sessionId, parts.messageId, parts.kind, parts.digest].join('|')
}

export type PreviewCandidate = { id: string; kind: StaticPreviewKind; digest: string }

// planMessagePreviews 按出现顺序挑出一条消息里真正要渲染的节点：同类型同内容只渲染
// 第一个，总数不超过 perMessage；其余保持源码。返回入选的 id 集合。
export function planMessagePreviews(candidates: PreviewCandidate[], max: number = PREVIEW_LIMITS.perMessage): Set<string> {
  const chosen = new Set<string>()
  const seen = new Set<string>()
  for (const c of candidates) {
    if (chosen.size >= max) break
    const key = `${c.kind}:${c.digest}`
    if (seen.has(key)) continue
    seen.add(key)
    chosen.add(c.id)
  }
  return chosen
}
