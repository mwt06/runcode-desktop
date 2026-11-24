// HTML/H5 静态预览：源码 → 惰性解析 → 正文清理 → 客户端固定外壳 → iframe.srcdoc。
//
// 生成内容一律不运行脚本：iframe 只给 allow-same-origin（父页面要读文档高度），
// 永远不加 allow-scripts——两者同时给，沙箱里的脚本就能拆掉自己的沙箱。外壳的 CSP
// 写在一切内容之前，是继 sandbox 与标记清理之后的第三道闸。
//
// 原文 <head> 里的 <style> 也要提取并单独过 CSS 白名单，整段丢掉会让 H5 只剩文字。
import DOMPurify, { type DOMPurify as Purifier } from 'dompurify'
import { PREVIEW_LIMITS, utf8Bytes } from './static-detect'
import { sanitizeDeclarations, sanitizeStylesheet } from './static-css'

export const HTML_PREVIEW_SANDBOX = 'allow-same-origin'

export const HTML_PREVIEW_CSP = [
  "default-src 'none'",
  "script-src 'none'",
  "connect-src 'none'",
  "object-src 'none'",
  "frame-src 'none'",
  "font-src 'none'",
  "media-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
  "style-src 'unsafe-inline'",
  'img-src data:',
].join('; ')

// 宿主外壳自带的样式排在生成样式之前：生成样式可以覆盖字体与背景这类观感，
// 但覆盖不了动画禁用与控件不可操作（!important）。
// height/min-height 用 !important：静态预览不是真机视口，压掉 100vh／满高 flex 对测高的破坏。
const SHELL_CSS = [
  'html,body{margin:0;min-height:auto!important;height:auto!important}',
  // position:relative 让绝对定位的子元素计入 body 的 scrollHeight，测高才准。
  'body{position:relative;padding:12px;font-family:system-ui,-apple-system,"Segoe UI","Microsoft YaHei","PingFang SC","Noto Sans CJK SC",sans-serif;color:#1f2328;background:#fff;overflow-wrap:anywhere}',
  '*,*::before,*::after{animation:none!important;transition:none!important}',
  'a,button,input,select,textarea,label,summary{pointer-events:none!important;cursor:default!important}',
  'img,svg{max-width:100%;height:auto}',
].join('\n')

/** 测试与调试可读外壳 CSS（含抗 100vh 规则）。 */
export function previewShellCss(): string {
  return SHELL_CSS
}

export type HtmlBodySanitizer = (html: string) => string

export type HtmlFailure = 'too-large' | 'parse-error' | 'empty' | 'needs-script'
export type HtmlResult =
  | { ok: true; srcdoc: string; blocked: { scripts: number; externalStyles: number } }
  | { ok: false; reason: HtmlFailure }

type Extracted = {
  lang: string
  styles: string[]
  body: string
  bodyClass: string
  bodyStyle: string
  scripts: number
  externalStyles: number
}

// extractHtml 用 DOMParser 解析：解析出的文档没有浏览上下文，脚本不运行、图片与
// 样式表都不加载。只取回字符串，不把任何节点插进活动文档。
export function extractHtml(source: string): Extracted | null {
  let doc: Document
  try {
    doc = new DOMParser().parseFromString(source, 'text/html')
  } catch {
    return null
  }
  const styles: string[] = []
  for (const el of Array.from(doc.querySelectorAll('style'))) {
    styles.push(el.textContent ?? '')
    el.remove()
  }
  const scripts = doc.querySelectorAll('script').length
  const externalStyles = doc.querySelectorAll('link[rel~="stylesheet" i]').length
  const body = doc.body
  return {
    lang: doc.documentElement.getAttribute('lang') ?? '',
    styles,
    body: body?.innerHTML ?? '',
    bodyClass: body?.getAttribute('class') ?? '',
    bodyStyle: body?.getAttribute('style') ?? '',
    scripts,
    externalStyles,
  }
}

// sanitizeInlineStyles 让正文里每个 style 属性都过 CSS 白名单。标记清理器会原样保留
// style 属性的内容，这一步不能交给它。
export function sanitizeInlineStyles(bodyHtml: string): string {
  const doc = new DOMParser().parseFromString(`<!doctype html><body>${bodyHtml}`, 'text/html')
  for (const el of Array.from(doc.body.querySelectorAll('[style]'))) {
    const css = sanitizeDeclarations(el.getAttribute('style') ?? '')
    if (css) el.setAttribute('style', css)
    else el.removeAttribute('style')
  }
  return doc.body.innerHTML
}

// hasVisibleContent：清理后的正文里至少要有文字或内嵌图形。只剩一个等脚本挂载的
// <div id="root"></div> 时不能把空白框当成预览成功。
export function hasVisibleContent(bodyHtml: string): boolean {
  const doc = new DOMParser().parseFromString(`<!doctype html><body>${bodyHtml}`, 'text/html')
  if ((doc.body.textContent ?? '').trim()) return true
  return doc.body.querySelector('img,svg,hr,table') !== null
}

function escapeAttr(value: string): string {
  return value.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

export function buildPreviewDocument(parts: { body: string; css: string; lang?: string; bodyClass?: string; bodyStyle?: string }): string {
  const lang = /^[A-Za-z]{1,8}(?:-[A-Za-z0-9]{1,8})*$/.test(parts.lang ?? '') ? parts.lang! : 'zh-CN'
  const cls = parts.bodyClass ? ` class="${escapeAttr(parts.bodyClass)}"` : ''
  const style = parts.bodyStyle ? ` style="${escapeAttr(parts.bodyStyle)}"` : ''
  return [
    `<!doctype html><html lang="${lang}"><head>`,
    `<meta http-equiv="Content-Security-Policy" content="${HTML_PREVIEW_CSP}">`,
    '<meta charset="utf-8">',
    `<style>${SHELL_CSS}</style>`,
    parts.css ? `<style>${parts.css}</style>` : '',
    `</head><body${cls}${style}>${parts.body}</body></html>`,
  ].join('')
}

// prepareStaticHtml 串起整条链路。正文清理器由调用方注入（DOMPurify 静态白名单），
// 本模块不自带一份手写的 HTML 清理逻辑。
export function prepareStaticHtml(source: string, sanitizeBody: HtmlBodySanitizer): HtmlResult {
  if (utf8Bytes(source) > PREVIEW_LIMITS.markupBytes) return { ok: false, reason: 'too-large' }
  const parts = extractHtml(source)
  if (!parts) return { ok: false, reason: 'parse-error' }
  const body = sanitizeInlineStyles(sanitizeBody(parts.body))
  if (!hasVisibleContent(body)) {
    return { ok: false, reason: parts.scripts > 0 || parts.externalStyles > 0 ? 'needs-script' : 'empty' }
  }
  const css = parts.styles.map(sanitizeStylesheet).filter(Boolean).join('\n')
  return {
    ok: true,
    srcdoc: buildPreviewDocument({
      body,
      css,
      lang: parts.lang,
      bodyClass: parts.bodyClass,
      bodyStyle: sanitizeDeclarations(parts.bodyStyle),
    }),
    blocked: { scripts: parts.scripts, externalStyles: parts.externalStyles },
  }
}

// measureDocumentHeight 量预览文档的内容高度。不能用 documentElement.scrollHeight：
// 内容比 iframe 矮时它返回视口高度（默认 150px），框就再也缩不回去（Chromium 实测）。
export function measureDocumentHeight(doc: Document): number {
  const body = doc.body
  if (!body) return 0
  return Math.ceil(Math.max(body.scrollHeight, body.getBoundingClientRect().height))
}

/**
 * 先把 iframe 拉到探测高度再量内容，避免短视口下 flex／100vh 主栏塌缩只剩底栏。
 * 探测后恢复调用方传入的高度字符串（通常交给 React style 再写回收敛值）。
 */
export function probeIframeContentHeight(
  frame: HTMLIFrameElement,
  probePx: number = PREVIEW_LIMITS.htmlMaxHeight,
): number {
  const doc = frame.contentDocument
  if (!doc?.body) return 0
  const prev = frame.style.height
  frame.style.height = `${Math.max(1, Math.ceil(probePx))}px`
  // 强制一次布局，再读 scrollHeight。
  void frame.getBoundingClientRect()
  const measured = measureDocumentHeight(doc)
  frame.style.height = prev
  return measured
}

// 正文的 DOMPurify 静态白名单。用独立实例：钩子挂在默认实例上会连带改变 Mermaid
// 内部对同一模块的清理行为。<style> 已由 extractHtml 提前取走，这里一律禁止。
const FORBID_TAGS = [
  'script', 'style', 'link', 'meta', 'base', 'iframe', 'frame', 'frameset', 'object', 'embed', 'applet',
  'form', 'video', 'audio', 'source', 'track', 'picture', 'canvas', 'dialog', 'template', 'slot', 'portal',
  'noscript', 'math', 'foreignObject',
]
const FORBID_ATTR = ['srcset', 'action', 'formaction', 'ping', 'target', 'autofocus', 'contenteditable', 'popover', 'download']
const CONTROLS = new Set(['BUTTON', 'INPUT', 'SELECT', 'TEXTAREA', 'OPTION', 'OPTGROUP', 'FIELDSET'])
// 承载地址的属性只放行文档内锚点与小型内嵌位图；远端、相对路径、file:、blob: 一律
// 不留。不能用 ALLOWED_URI_REGEXP 表达：DOMPurify 把它套在所有非 URI 安全属性上，
// width、fill 这类普通值也会被一并删掉。
const URL_ATTRS = new Set(['src', 'href', 'xlink:href', 'poster', 'background', 'cite', 'longdesc', 'data', 'codebase', 'manifest'])
const ALLOWED_URL = /^(?:#[A-Za-z_][\w:.-]*$|data:image\/(?:png|jpeg|gif|webp);base64,)/i
const LOCAL_CSS_URL = /url\(\s*(["']?)#[A-Za-z_][\w:.-]*\1\s*\)/gi

let purifier: Purifier | null = null

function staticPurifier(): Purifier {
  if (purifier) return purifier
  const p = DOMPurify(window)
  p.addHook('uponSanitizeAttribute', (_node, data) => {
    const value = data.attrValue.trim()
    if (URL_ATTRS.has(data.attrName) && !ALLOWED_URL.test(value)) data.keepAttr = false
    else if (data.attrName !== 'style' && /url\s*\(/i.test(value.replace(LOCAL_CSS_URL, ''))) data.keepAttr = false
  })
  p.addHook('afterSanitizeAttributes', (node) => {
    // 超链接只当文字：去掉 href，SVG 内的 #id 引用（use/渐变等）不受影响。
    if (node.nodeName === 'A' || node.nodeName === 'AREA') node.removeAttribute('href')
    // 控件按静态外观展示：没有表单可提交，再加 disabled 防止聚焦输入。
    if (CONTROLS.has(node.nodeName)) node.setAttribute('disabled', '')
  })
  purifier = p
  return p
}

export function sanitizeHtmlBody(html: string): string {
  return staticPurifier().sanitize(html, {
    USE_PROFILES: { html: true, svg: true, svgFilters: true },
    FORBID_TAGS,
    FORBID_ATTR,
    ALLOW_DATA_ATTR: false,
    ALLOW_UNKNOWN_PROTOCOLS: false,
  })
}

// renderStaticHtml：消息内 HTML 预览的完整入口（DOMPurify 正文 + CSS 白名单 + 固定外壳）。
export function renderStaticHtml(source: string): HtmlResult {
  return prepareStaticHtml(source, sanitizeHtmlBody)
}

// 自动高度的收敛控制。父页面在 load、容器宽度变化和资源加载后测量 scrollHeight，
// 交给 observe：高度在上限内且趋于稳定就跟随；超过上限，或连续增长不收敛（`100vh`、
// 百分比高度会让「放大 iframe → 内容随之变高」形成反馈），就停止放大、固定高度并
// 改为容器内滚动。
export type HeightDecision = { height: number; scroll: boolean }

export function createHeightGovernor(opts: { max?: number; min?: number; maxGrowthSteps?: number } = {}) {
  const max = opts.max ?? PREVIEW_LIMITS.htmlMaxHeight
  const min = opts.min ?? 48
  const maxGrowth = opts.maxGrowthSteps ?? 6
  let current = 0
  let growth = 0
  let frozen: HeightDecision | null = null
  return {
    observe(measured: number): HeightDecision {
      if (frozen) return frozen
      const h = Math.max(min, Math.ceil(Number.isFinite(measured) ? measured : min))
      if (h > max) return (frozen = { height: max, scroll: true })
      // 测量值紧贴上次设定的高度往上涨（差值很小），是内容随容器一起长的典型表现。
      growth = h > current && current > 0 && h - current <= 32 ? growth + 1 : 0
      if (growth >= maxGrowth) return (frozen = { height: current, scroll: true })
      current = h
      return { height: h, scroll: false }
    },
    reset() {
      current = 0
      growth = 0
      frozen = null
    },
  }
}
