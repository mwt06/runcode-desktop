// SVG 静态预览的清理：源码 → 惰性 XML 解析 → 按白名单重建 → 序列化。产物交给
// <img>（Blob URL）显示，不进宿主 DOM。<img> 加载的 SVG 本身不跑脚本、不发外部请求，
// 这里的白名单是第二道防线，同时负责把尺寸收敛到消息栏能放下的范围。
//
// 重建而不是就地删：输出里只会出现我们亲手创建的 SVG 命名空间元素，原文里的命名
// 空间声明、处理指令、DTD 与未知结构都不会被带出去。模型常省略 xmlns，按 SVG 处理。
import { PREVIEW_LIMITS, utf8Bytes } from './static-detect'
import { sanitizeDeclarations, sanitizeStylesheet } from './static-css'

const SVG_NS = 'http://www.w3.org/2000/svg'
const XLINK_NS = 'http://www.w3.org/1999/xlink'

export const SVG_DEFAULT_SIZE = { width: 600, height: 400 }
export const SVG_MAX_DIMENSION = 4096
const MAX_SOURCE_ELEMENTS = 20000
const MAX_FILTER_PRIMITIVES = 32

const CONTAINERS = ['svg', 'g', 'defs', 'symbol', 'clipPath', 'mask', 'pattern', 'marker', 'linearGradient', 'radialGradient', 'filter']
const SHAPES = ['path', 'rect', 'circle', 'ellipse', 'line', 'polyline', 'polygon', 'use', 'image']
const TEXT = ['text', 'tspan', 'textPath', 'title', 'desc']
const FILTER_PRIMITIVES = new Set([
  'feGaussianBlur', 'feOffset', 'feBlend', 'feFlood', 'feComposite', 'feMerge', 'feMergeNode',
  'feColorMatrix', 'feDropShadow', 'feMorphology',
])
const ELEMENTS = new Set([...CONTAINERS, ...SHAPES, ...TEXT, 'stop', 'style', ...FILTER_PRIMITIVES])
// 链接容器只去壳、保留里面的图形：<a> 包着的节点仍然是图的一部分，只是不能点。
const UNWRAP = new Set(['a', 'switch'])
const TEXT_HOLDERS = new Set([...TEXT, 'style'])
const GRAPHIC = new Set(['path', 'rect', 'circle', 'ellipse', 'line', 'polyline', 'polygon', 'use', 'image'])

const ATTRS = new Set([
  'id', 'class', 'x', 'y', 'x1', 'y1', 'x2', 'y2', 'cx', 'cy', 'r', 'rx', 'ry', 'fx', 'fy', 'fr',
  'width', 'height', 'd', 'points', 'transform', 'transform-origin', 'viewBox', 'preserveAspectRatio', 'pathLength',
  'fill', 'fill-opacity', 'fill-rule', 'stroke', 'stroke-width', 'stroke-opacity', 'stroke-dasharray',
  'stroke-dashoffset', 'stroke-linecap', 'stroke-linejoin', 'stroke-miterlimit', 'opacity', 'color',
  'display', 'visibility', 'overflow', 'clip-path', 'clip-rule', 'mask', 'filter', 'vector-effect', 'paint-order',
  'shape-rendering', 'text-rendering', 'mix-blend-mode', 'isolation',
  'marker-start', 'marker-mid', 'marker-end', 'markerWidth', 'markerHeight', 'markerUnits', 'refX', 'refY', 'orient',
  'font-family', 'font-size', 'font-weight', 'font-style', 'font-variant', 'text-anchor', 'dominant-baseline',
  'alignment-baseline', 'baseline-shift', 'letter-spacing', 'word-spacing', 'text-decoration', 'writing-mode',
  'dx', 'dy', 'rotate', 'textLength', 'lengthAdjust', 'startOffset', 'xml:space',
  'offset', 'stop-color', 'stop-opacity', 'gradientUnits', 'gradientTransform', 'spreadMethod',
  'patternUnits', 'patternContentUnits', 'patternTransform', 'clipPathUnits', 'maskUnits', 'maskContentUnits',
  'filterUnits', 'primitiveUnits', 'in', 'in2', 'result', 'stdDeviation', 'mode', 'operator', 'radius',
  'k1', 'k2', 'k3', 'k4', 'values', 'type', 'flood-color', 'flood-opacity',
  'role', 'aria-label', 'aria-hidden',
])

const LOCAL_URL = /url\(\s*(["']?)#[A-Za-z_][\w:.-]*\1\s*\)/gi
const LOCAL_HREF = /^#[A-Za-z_][\w:.-]*$/
const DATA_IMAGE = /^data:image\/(?:png|jpeg|gif|webp);base64,[A-Za-z0-9+/=\s]+$/

export type SvgFailure = 'too-large' | 'unsafe-dtd' | 'parse-error' | 'not-svg' | 'too-complex' | 'empty'
export type SvgResult = { ok: true; svg: string; width: number; height: number } | { ok: false; reason: SvgFailure }

export function sanitizeSvg(source: string): SvgResult {
  if (utf8Bytes(source) > PREVIEW_LIMITS.markupBytes) return { ok: false, reason: 'too-large' }
  // 内部实体可以指数膨胀（billion laughs）；正常的图用不到 DTD 子集。
  if (/<!ENTITY/i.test(source) || /<!DOCTYPE[^>]*\[/i.test(source)) return { ok: false, reason: 'unsafe-dtd' }
  let parsed: Document
  try {
    parsed = new DOMParser().parseFromString(source.trim(), 'image/svg+xml')
  } catch {
    return { ok: false, reason: 'parse-error' }
  }
  if (parsed.getElementsByTagName('parsererror').length > 0) return { ok: false, reason: 'parse-error' }
  const root = parsed.documentElement
  if (!root || root.localName !== 'svg' || !isSvgNamespace(root)) return { ok: false, reason: 'not-svg' }

  const out = document.implementation.createDocument(SVG_NS, 'svg', null)
  const outRoot = out.documentElement
  const budget = { visited: 0, filters: 0, graphics: 0 }
  copyAttributes(root, outRoot)
  if (!copyChildren(root, outRoot, out, budget)) return { ok: false, reason: 'too-complex' }
  if (budget.graphics === 0) return { ok: false, reason: 'empty' }

  const { width, height } = normalizeSize(outRoot)
  return { ok: true, svg: new XMLSerializer().serializeToString(outRoot), width, height }
}

function isSvgNamespace(el: Element): boolean {
  return el.namespaceURI === SVG_NS || el.namespaceURI === null
}

type Budget = { visited: number; filters: number; graphics: number }

function copyChildren(from: Element, to: Element, doc: XMLDocument, budget: Budget): boolean {
  for (const node of Array.from(from.childNodes)) {
    if (node.nodeType === Node.TEXT_NODE || node.nodeType === Node.CDATA_SECTION_NODE) {
      if (TEXT_HOLDERS.has(to.localName)) {
        const text = to.localName === 'style' ? sanitizeStylesheet(node.nodeValue ?? '') : node.nodeValue ?? ''
        if (text) to.appendChild(doc.createTextNode(text))
        if (to.localName !== 'style' && text.trim() && (to.localName === 'text' || to.localName === 'tspan' || to.localName === 'textPath')) budget.graphics++
      }
      continue
    }
    if (node.nodeType !== Node.ELEMENT_NODE) continue
    const el = node as Element
    if (++budget.visited > MAX_SOURCE_ELEMENTS) return false
    if (!isSvgNamespace(el)) continue
    const name = el.localName
    if (UNWRAP.has(name)) {
      if (!copyChildren(el, to, doc, budget)) return false
      continue
    }
    if (!ELEMENTS.has(name)) continue // script、foreignObject、动画、iframe 等连同子树丢弃
    if (FILTER_PRIMITIVES.has(name) && ++budget.filters > MAX_FILTER_PRIMITIVES) return false
    const copy = doc.createElementNS(SVG_NS, name)
    copyAttributes(el, copy)
    if (name === 'image' && !copy.hasAttribute('href')) continue
    if (name === 'use' && !copy.hasAttribute('href')) continue
    if (GRAPHIC.has(name)) budget.graphics++
    if (!copyChildren(el, copy, doc, budget)) return false
    to.appendChild(copy)
  }
  return true
}

function copyAttributes(from: Element, to: Element) {
  for (const attr of Array.from(from.attributes)) {
    const name = attr.name
    const value = attr.value
    if (/javascript:|vbscript:/i.test(withoutBlanks(value))) continue
    if (name === 'href' || (name === 'xlink:href' && (attr.namespaceURI === XLINK_NS || attr.namespaceURI === null))) {
      const href = value.trim()
      if (to.localName === 'image' ? DATA_IMAGE.test(href) : LOCAL_HREF.test(href)) to.setAttribute('href', href)
      continue
    }
    if (name === 'style') {
      const css = sanitizeDeclarations(value)
      if (css) to.setAttribute('style', css)
      continue
    }
    if (!ATTRS.has(name)) continue
    if (/url\s*\(/i.test(value.replace(LOCAL_URL, ''))) continue
    if (/[<>]/.test(value)) continue
    if (name === 'xml:space') to.setAttributeNS('http://www.w3.org/XML/1998/namespace', 'xml:space', value)
    else to.setAttribute(name, value)
  }
}

// 浏览器解析 URL 时会忽略空白与控制字符，`java\tscript:` 照样生效，比较前先去掉。
function withoutBlanks(value: string): string {
  let out = ''
  for (const ch of value) if (ch.charCodeAt(0) > 0x20) out += ch
  return out
}

// 只认无单位或 px 的绝对长度；百分比、em 等相对单位在 <img> 里没有可靠含义。
export function parseLength(value: string | null): number | null {
  if (!value) return null
  const m = /^\s*(\d*\.?\d+(?:e[+-]?\d+)?)\s*(px)?\s*$/i.exec(value)
  if (!m) return null
  const n = Number(m[1])
  return Number.isFinite(n) && n > 0 ? n : null
}

export function parseViewBox(value: string | null): { x: number; y: number; w: number; h: number } | null {
  if (!value) return null
  const parts = value.trim().split(/[\s,]+/).map(Number)
  if (parts.length !== 4 || parts.some((n) => !Number.isFinite(n))) return null
  const [x, y, w, h] = parts
  return w > 0 && h > 0 ? { x, y, w, h } : null
}

// normalizeSize 给根元素写上确定的宽高（必要时补 viewBox），并把最长边夹到上限。
// 缺少任何尺寸信息时用受限默认值，宁可裁边也不让巨大坐标撑开消息栏。
function normalizeSize(root: Element): { width: number; height: number } {
  const vb = parseViewBox(root.getAttribute('viewBox'))
  let w = parseLength(root.getAttribute('width'))
  let h = parseLength(root.getAttribute('height'))
  if (vb) {
    if (!w && !h) { w = vb.w; h = vb.h }
    else if (!w) w = (h! * vb.w) / vb.h
    else if (!h) h = (w * vb.h) / vb.w
  } else if (w && h) {
    root.setAttribute('viewBox', `0 0 ${w} ${h}`)
  }
  if (!w || !h) {
    w = SVG_DEFAULT_SIZE.width
    h = SVG_DEFAULT_SIZE.height
  }
  const scale = Math.min(1, SVG_MAX_DIMENSION / Math.max(w, h))
  const width = round(w * scale)
  const height = round(h * scale)
  root.setAttribute('width', String(width))
  root.setAttribute('height', String(height))
  return { width, height }
}

function round(n: number): number {
  return Math.max(1, Math.round(n * 100) / 100)
}
