// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import {
  buildPreviewDocument, createHeightGovernor, extractHtml, hasVisibleContent, measureDocumentHeight, prepareStaticHtml,
  previewShellCss, probeIframeContentHeight, renderStaticHtml, sanitizeHtmlBody,
  HTML_PREVIEW_CSP, HTML_PREVIEW_SANDBOX,
} from './static-html'
import { PREVIEW_LIMITS } from './static-detect'

// 测试替身：只删 <script>，用来验证外壳与提取的结构。真实的正文清理（DOMPurify 静态
// 白名单）单独测试；这里不能拿替身证明 X01/X02。
const dropScripts = (html: string) => {
  const doc = new DOMParser().parseFromString(`<body>${html}`, 'text/html')
  doc.querySelectorAll('script').forEach((s) => s.remove())
  return doc.body.innerHTML
}

const PAGE = `<!doctype html><html lang="zh-CN"><head>
<style>.grid{display:grid;grid-template-columns:repeat(2,1fr);gap:8px} .box{border:1px solid #333;background:url(http://x/bg.png)}</style>
<link rel="stylesheet" href="https://cdn.test/x.css">
</head><body class="page" style="background:#f6f8fa;behavior:x">
<div class="grid"><div class="box">前端</div><div class="box">后端</div></div>
<style>.box{padding:6px}</style>
<script>document.body.innerHTML=''</script>
</body></html>`

describe('policy constants', () => {
  it('never grants scripts to the preview iframe', () => {
    expect(HTML_PREVIEW_SANDBOX.split(/\s+/)).toEqual(['allow-same-origin'])
  })
  it('locks down every fetch directive except inline style and data images', () => {
    for (const d of ["default-src 'none'", "script-src 'none'", "connect-src 'none'", "object-src 'none'", "frame-src 'none'", "base-uri 'none'", "form-action 'none'", "style-src 'unsafe-inline'", 'img-src data:']) {
      expect(HTML_PREVIEW_CSP).toContain(d)
    }
  })
})

describe('extractHtml', () => {
  it('pulls head and body styles out, counts blocked scripts and external sheets', () => {
    const parts = extractHtml(PAGE)!
    expect(parts.lang).toBe('zh-CN')
    expect(parts.styles).toHaveLength(2)
    expect(parts.body).not.toContain('<style')
    expect(parts.scripts).toBe(1)
    expect(parts.externalStyles).toBe(1)
    expect(parts.bodyClass).toBe('page')
  })
  it('wraps bare fragments into a body', () => {
    expect(extractHtml('<div>片段</div>')!.body).toBe('<div>片段</div>')
  })
})

describe('buildPreviewDocument', () => {
  it('puts the CSP before any generated content and escapes body attributes', () => {
    const doc = buildPreviewDocument({ body: '<p>x</p>', css: '.a{color:red}', bodyClass: 'a" onload="x', bodyStyle: 'color:red' })
    const cspAt = doc.indexOf('Content-Security-Policy')
    expect(cspAt).toBeGreaterThan(0)
    expect(cspAt).toBeLessThan(doc.indexOf('.a{color:red}'))
    expect(cspAt).toBeLessThan(doc.indexOf('<p>x</p>'))
    expect(doc).toContain('class="a&quot; onload=&quot;x"')
    const parsed = new DOMParser().parseFromString(doc, 'text/html')
    expect(parsed.body.getAttribute('onload')).toBeNull()
    expect(parsed.querySelector('meta[http-equiv="Content-Security-Policy"]')?.getAttribute('content')).toBe(HTML_PREVIEW_CSP)
  })
  it('falls back to zh-CN for malformed lang values', () => {
    expect(buildPreviewDocument({ body: '', css: '', lang: 'en"><script>' })).toContain('<html lang="zh-CN">')
    expect(buildPreviewDocument({ body: '', css: '', lang: 'en-US' })).toContain('<html lang="en-US">')
  })
  it('keeps host rules that disable animation and control interaction', () => {
    const doc = buildPreviewDocument({ body: '', css: '' })
    expect(doc).toContain('animation:none!important')
    expect(doc).toContain('pointer-events:none!important')
  })
  it('forces html/body height auto so 100vh layouts do not lock measure', () => {
    const css = previewShellCss()
    expect(css).toMatch(/min-height:\s*auto\s*!important/)
    expect(css).toMatch(/height:\s*auto\s*!important/)
    expect(buildPreviewDocument({ body: '', css: '' })).toContain('height:auto!important')
  })
})

describe('prepareStaticHtml', () => {
  it('produces a static document with sanitized styles (F03)', () => {
    const r = prepareStaticHtml(PAGE, dropScripts)
    if (!r.ok) throw new Error(r.reason)
    expect(r.blocked).toEqual({ scripts: 1, externalStyles: 1 })
    expect(r.srcdoc).toContain('display:grid')
    expect(r.srcdoc).toContain('.box{padding:6px}')
    expect(r.srcdoc).not.toContain('bg.png')
    expect(r.srcdoc).not.toContain('cdn.test')
    expect(r.srcdoc).toContain('style="background:#f6f8fa"')
    expect(r.srcdoc).toContain('前端')
  })
  it('reports pages that only render through scripts instead of showing a blank box', () => {
    const spa = '<!doctype html><html><head><script src="https://cdn/react.js"></script></head><body><div id="root"></div><script>mount()</script></body></html>'
    expect(prepareStaticHtml(spa, dropScripts)).toEqual({ ok: false, reason: 'needs-script' })
    expect(prepareStaticHtml('<html><body>   </body></html>', dropScripts)).toEqual({ ok: false, reason: 'empty' })
  })
  it('refuses oversized input before parsing', () => {
    expect(prepareStaticHtml('<p>' + 'x'.repeat(256 * 1024) + '</p>', dropScripts)).toEqual({ ok: false, reason: 'too-large' })
  })
  it('runs inline style attributes through the CSS allowlist after the body sanitizer', () => {
    const r = prepareStaticHtml('<html><body><div style="color:red;background:url(https://x/a.png)">a</div><p style="animation:x 1s">b</p></body></html>', (b) => b)
    if (!r.ok) throw new Error(r.reason)
    expect(r.srcdoc).toContain('<div style="color:red">a</div>')
    expect(r.srcdoc).toContain('<p>b</p>')
    expect(r.srcdoc).not.toContain('a.png')
  })
  it('applies the injected sanitizer to the body', () => {
    const r = prepareStaticHtml('<html><body><p>a</p></body></html>', () => '<p>cleaned</p>')
    expect(r.ok && r.srcdoc).toContain('<p>cleaned</p>')
  })
})

describe('sanitizeHtmlBody / renderStaticHtml (DOMPurify static allowlist)', () => {
  const bodyOf = (srcdoc: string) => new DOMParser().parseFromString(srcdoc, 'text/html').body

  it('removes scripts, event handlers and javascript URLs (X01)', () => {
    const out = sanitizeHtmlBody('<p onclick="x()">a</p><script>alert(1)</script><img src="x" onerror="alert(2)"><a href="javascript:alert(3)">b</a><svg><script>alert(4)</script><rect width="1" height="1" onload="x()"/></svg>')
    expect(out).not.toMatch(/script|onclick|onerror|onload|javascript/i)
    expect(out).toContain('<rect')
  })
  it('drops forms, frames, meta refresh, base and popups (X02)', () => {
    const out = sanitizeHtmlBody('<form action="https://x"><input name="q"><button formaction="https://y">go</button></form><iframe src="https://x"></iframe><meta http-equiv="refresh" content="0;url=https://x"><base href="https://x/"><object data="x"></object><embed src="x"><dialog open>d</dialog><a href="https://x" target="_blank">link</a>')
    expect(out).not.toMatch(/<form|<iframe|<meta|<base|<object|<embed|<dialog|action=|target=|https:\/\/x/i)
    expect(out).toContain('link')
  })
  it('keeps controls only as disabled static visuals', () => {
    const body = bodyOf(`<body>${sanitizeHtmlBody('<button>提交</button><input value="v"><select><option>a</option></select><textarea>t</textarea>')}</body>`)
    for (const el of Array.from(body.querySelectorAll('button,input,select,textarea'))) expect(el.hasAttribute('disabled')).toBe(true)
    expect(body.textContent).toContain('提交')
  })
  it('keeps only data images and in-document references (X03)', () => {
    const out = sanitizeHtmlBody('<img src="https://x/a.png"><img src="a.png"><img src="file:///c:/a.png"><img src="data:image/png;base64,iVBORw0KGgo="><img srcset="a.png 1x"><svg><defs><linearGradient id="g"></linearGradient></defs><rect width="1" height="1" fill="url(#g)"></rect><use href="#g"></use><image href="https://x/i.png"></image></svg>')
    const body = bodyOf(`<body>${out}</body>`)
    const srcs = Array.from(body.querySelectorAll('img')).map((i) => i.getAttribute('src'))
    expect(srcs.filter(Boolean)).toEqual(['data:image/png;base64,iVBORw0KGgo='])
    expect(out).not.toMatch(/https:|file:|a\.png|srcset/)
    expect(body.querySelector('rect')?.getAttribute('fill')).toBe('url(#g)')
    expect(body.querySelector('rect')?.getAttribute('width')).toBe('1')
    expect(sanitizeHtmlBody('<svg><rect width="2" fill="url(https://x/p.svg#a)"/></svg>')).not.toContain('https')
    expect(sanitizeHtmlBody('<table><tr><td colspan="2" width="40">c</td></tr></table>')).toContain('colspan="2"')
    // DOMPurify 默认不放行 <use>（历史上的外部引用漏洞），HTML 内联 SVG 不支持它。
    expect(body.querySelector('use')).toBeNull()
  })
  it('drops SVG foreignObject and animation inside HTML', () => {
    const out = sanitizeHtmlBody('<svg><foreignObject><div>x</div></foreignObject><rect width="1" height="1"><animate attributeName="x" to="9"/></rect></svg>')
    expect(out).not.toMatch(/foreignObject|animate/i)
  })
  it('does not leak its hooks into the default DOMPurify instance', async () => {
    const { default: shared } = await import('dompurify')
    sanitizeHtmlBody('<a href="#x">a</a>')
    expect(shared.sanitize('<a href="https://ok.test">a</a>')).toContain('href="https://ok.test"')
  })
  it('renders a complete static architecture page end to end (F03)', () => {
    const r = renderStaticHtml(PAGE)
    if (!r.ok) throw new Error(r.reason)
    const body = bodyOf(r.srcdoc)
    expect(body.querySelectorAll('.box')).toHaveLength(2)
    expect(r.srcdoc).not.toMatch(/<script|innerHTML|bg\.png|cdn\.test/)
  })
  it('accepts an explicit preview fragment', () => {
    const r = renderStaticHtml('<div style="display:flex;gap:8px"><span>甲</span><span>乙</span></div>')
    expect(r.ok && r.srcdoc).toContain('display:flex')
  })
})

describe('hasVisibleContent', () => {
  it('accepts text or inline graphics, rejects empty mount points', () => {
    expect(hasVisibleContent('<div>文字</div>')).toBe(true)
    expect(hasVisibleContent('<svg viewBox="0 0 1 1"></svg>')).toBe(true)
    expect(hasVisibleContent('<img src="data:image/png;base64,AAAA">')).toBe(true)
    expect(hasVisibleContent('<div id="root"></div>')).toBe(false)
  })
})

describe('measureDocumentHeight', () => {
  it('reads the body box, not the viewport-floored documentElement.scrollHeight', () => {
    const doc = new DOMParser().parseFromString('<body><p>x</p></body>', 'text/html')
    expect(measureDocumentHeight(doc)).toBeGreaterThanOrEqual(0)
    const empty = document.implementation.createHTMLDocument('')
    empty.body.remove()
    expect(measureDocumentHeight(empty)).toBe(0)
  })
})

describe('probeIframeContentHeight', () => {
  it('temporarily sets probe height then restores the previous inline height', () => {
    const frame = document.createElement('iframe')
    document.body.appendChild(frame)
    const doc = frame.contentDocument
    if (!doc) {
      frame.remove()
      return
    }
    doc.open()
    doc.write('<!doctype html><body><div style="height:500px">list</div><div>cart</div></body>')
    doc.close()
    frame.style.height = '120px'
    const probed = probeIframeContentHeight(frame, PREVIEW_LIMITS.htmlMaxHeight)
    expect(Number.isFinite(probed)).toBe(true)
    expect(probed).toBeGreaterThanOrEqual(0)
    expect(frame.style.height).toBe('120px')
    frame.remove()
  })
})

describe('createHeightGovernor (L04)', () => {
  it('follows content height within the cap', () => {
    const g = createHeightGovernor({ max: 1000 })
    expect(g.observe(300)).toEqual({ height: 300, scroll: false })
    expect(g.observe(420)).toEqual({ height: 420, scroll: false })
    expect(g.observe(400)).toEqual({ height: 400, scroll: false })
  })
  it('caps oversized pages and switches to inner scrolling for good', () => {
    const g = createHeightGovernor({ max: 1000 })
    expect(g.observe(5000)).toEqual({ height: 1000, scroll: true })
    expect(g.observe(200)).toEqual({ height: 1000, scroll: true })
    g.reset()
    expect(g.observe(200)).toEqual({ height: 200, scroll: false })
  })
  it('stops a 100vh-style feedback loop instead of growing forever', () => {
    const g = createHeightGovernor({ max: 2400, maxGrowthSteps: 4 })
    let last = g.observe(300)
    for (let i = 0; i < 20 && !last.scroll; i++) last = g.observe(last.height + 4)
    expect(last.scroll).toBe(true)
    expect(last.height).toBeLessThan(400)
  })
  it('applies a minimum height and tolerates junk measurements', () => {
    const g = createHeightGovernor({ min: 48 })
    expect(g.observe(0)).toEqual({ height: 48, scroll: false })
    expect(g.observe(Number.NaN)).toEqual({ height: 48, scroll: false })
  })
})
