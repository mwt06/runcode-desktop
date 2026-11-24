// 静态预览里的 CSS 白名单：SVG 的 <style>/style 属性与 HTML 预览的样式共用这一份。
// DOMPurify 之类的标记清理器不检查 CSS 内容，样式必须单独过一遍。
//
// 策略是"认不准就丢"：只按声明/规则为粒度丢弃，不改写值。网络请求的真正闸门是
// iframe 的 CSP 与 <img> 加载 SVG 时的禁网语义；这里拦的是 @import、外部 url()、
// 动画以及会把 </style> 带进文档的写法，是第二道防线。

const MAX_DECL_VALUE = 2000
const MAX_LENGTH_NUMBER = 20000
const MAX_MEDIA_DEPTH = 2

// 允许出现在值里的函数。url() 单独处理：只放行指向本文档内 #id 的引用。
const FUNCTIONS = new Set([
  'rgb', 'rgba', 'hsl', 'hsla', 'hwb', 'lab', 'lch', 'oklab', 'oklch', 'color-mix',
  'calc', 'min', 'max', 'clamp', 'var',
  'linear-gradient', 'radial-gradient', 'conic-gradient',
  'repeating-linear-gradient', 'repeating-radial-gradient', 'repeating-conic-gradient',
  'translate', 'translatex', 'translatey', 'translatez', 'translate3d',
  'rotate', 'rotatex', 'rotatey', 'rotatez', 'rotate3d',
  'scale', 'scalex', 'scaley', 'scalez', 'scale3d',
  'skew', 'skewx', 'skewy', 'matrix', 'matrix3d', 'perspective',
  'repeat', 'minmax', 'fit-content',
  'blur', 'brightness', 'contrast', 'drop-shadow', 'grayscale', 'hue-rotate', 'invert', 'opacity', 'saturate', 'sepia',
  'inset', 'circle', 'ellipse', 'polygon',
])

// 动画与过渡不属于静态内容；其余是历史上能执行代码或加载资源的私有属性。
const DENIED_PROPERTY = /^(?:-[a-z]+-)?(?:animation|transition)(?:-|$)|^(?:-moz-binding|behavior|src)$/

const LOCAL_URL = /url\(\s*(["']?)#[A-Za-z_][\w:.-]*\1\s*\)/gi

export function stripCssComments(text: string): string {
  let out = ''
  let quote = ''
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]
    if (quote) {
      out += ch
      if (ch === '\\' && i + 1 < text.length) out += text[++i]
      else if (ch === quote) quote = ''
    } else if (ch === '"' || ch === "'") {
      quote = ch
      out += ch
    } else if (ch === '/' && text[i + 1] === '*') {
      const end = text.indexOf('*/', i + 2)
      i = end < 0 ? text.length : end + 1
      out += ' '
    } else {
      out += ch
    }
  }
  return out
}

// splitTop 在顶层（不在字符串、括号或花括号内）按分隔符切分。
function splitTop(text: string, sep: string): string[] {
  const parts: string[] = []
  let depth = 0
  let quote = ''
  let cur = ''
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]
    if (quote) {
      if (ch === '\\' && i + 1 < text.length) { cur += ch + text[++i]; continue }
      if (ch === quote) quote = ''
    } else if (ch === '"' || ch === "'") quote = ch
    else if (ch === '(' || ch === '{' || ch === '[') depth++
    else if (ch === ')' || ch === '}' || ch === ']') depth = Math.max(0, depth - 1)
    else if (ch === sep && depth === 0) { parts.push(cur); cur = ''; continue }
    cur += ch
  }
  parts.push(cur)
  return parts
}

function withoutStrings(value: string): string {
  return value.replace(/"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'/g, '""')
}

function safeValue(value: string): boolean {
  if (!value || value.length > MAX_DECL_VALUE) return false
  // 反斜杠转义能把 url( 或 </style> 拆成认不出的形状；`<` 只为 </style> 而拒。
  if (/[\\<{}]/.test(value)) return false
  const bare = withoutStrings(value.replace(LOCAL_URL, ' '))
  if (/expression\s*\(|javascript:/i.test(bare)) return false
  const fn = /([a-z][a-z0-9-]*)\s*\(/gi
  for (let m = fn.exec(bare); m; m = fn.exec(bare)) {
    const name = m[1].toLowerCase()
    // CSS 里函数名与括号之间不能有空白；有空白的不是函数，但也不是合法值，直接拒。
    if (m[0].length !== name.length + 1 || !FUNCTIONS.has(name)) return false
  }
  const num = /(-?\d*\.?\d+(?:e[+-]?\d+)?)(px|em|rem|vh|vw|vmin|vmax|%|pt|cm|mm|in)\b/gi
  for (let m = num.exec(bare); m; m = num.exec(bare)) {
    if (Math.abs(Number(m[1])) > MAX_LENGTH_NUMBER) return false
  }
  return true
}

// sanitizeDeclarations 清理一段声明列表（style 属性或规则体），返回 `prop:value;…`。
export function sanitizeDeclarations(text: string): string {
  const out: string[] = []
  for (const decl of splitTop(stripCssComments(text), ';')) {
    const colon = decl.indexOf(':')
    if (colon <= 0) continue
    const prop = decl.slice(0, colon).trim().toLowerCase()
    const value = decl.slice(colon + 1).trim()
    if (!/^(?:--[\w-]+|-?[a-z][a-z0-9-]*)$/.test(prop) || DENIED_PROPERTY.test(prop)) continue
    if (!safeValue(value)) continue
    out.push(`${prop}:${value}`)
  }
  return out.join(';')
}

type CssItem = { prelude: string; body: string | null }

// 顶层切成「前导 + 块」：`a{…}` 与 `@import …;` 两种形状。块没闭合的整段丢弃。
function splitRules(text: string): CssItem[] {
  const items: CssItem[] = []
  let prelude = ''
  let i = 0
  while (i < text.length) {
    const ch = text[i]
    if (ch === '"' || ch === "'") {
      const end = skipString(text, i)
      prelude += text.slice(i, end)
      i = end
    } else if (ch === ';') {
      if (prelude.trim()) items.push({ prelude, body: null })
      prelude = ''
      i++
    } else if (ch === '{') {
      const end = matchBrace(text, i)
      if (end < 0) return items
      items.push({ prelude, body: text.slice(i + 1, end) })
      prelude = ''
      i = end + 1
    } else if (ch === '}') {
      prelude = '' // 多余的右括号：丢掉当前残片，从下一条重新开始
      i++
    } else {
      prelude += ch
      i++
    }
  }
  return items
}

function skipString(text: string, start: number): number {
  const quote = text[start]
  for (let i = start + 1; i < text.length; i++) {
    if (text[i] === '\\') i++
    else if (text[i] === quote) return i + 1
  }
  return text.length
}

function matchBrace(text: string, open: number): number {
  let depth = 0
  for (let i = open; i < text.length; i++) {
    const ch = text[i]
    if (ch === '"' || ch === "'") { i = skipString(text, i) - 1; continue }
    if (ch === '{') depth++
    else if (ch === '}' && --depth === 0) return i
  }
  return -1
}

function safeSelector(sel: string): boolean {
  return !!sel && sel.length <= 1000 && !/[\\<@{}]/.test(sel) && !/javascript:/i.test(sel)
}

// sanitizeStylesheet 清理一整段样式表。只保留普通规则与 @media；@import、@font-face、
// @keyframes、@namespace 等一律丢弃。
export function sanitizeStylesheet(text: string): string {
  return sanitizeRules(stripCssComments(text), 0)
}

function sanitizeRules(text: string, depth: number): string {
  const out: string[] = []
  for (const { prelude, body } of splitRules(text)) {
    const head = prelude.trim()
    if (body === null) continue
    if (head.startsWith('@')) {
      const name = /^@([\w-]+)/.exec(head)?.[1]?.toLowerCase()
      const query = head.slice(name ? name.length + 1 : 1).trim()
      if (name !== 'media' || depth >= MAX_MEDIA_DEPTH || !/^[\w\s(),:.%/=>-]*$/.test(query)) continue
      const inner = sanitizeRules(body, depth + 1)
      if (inner) out.push(`@media ${query}{${inner}}`)
      continue
    }
    if (!safeSelector(head)) continue
    const decls = sanitizeDeclarations(body)
    if (decls) out.push(`${head}{${decls}}`)
  }
  return out.join('\n')
}
