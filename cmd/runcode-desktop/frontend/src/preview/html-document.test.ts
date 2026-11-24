// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
// @ts-expect-error jsdom 未附带类型；仅在测试中执行受控 fixture，不执行用户代码。
import { JSDOM, VirtualConsole } from 'jsdom'
import { prepareHtmlPreview, readHtmlPreviewMessage, HTML_SCRIPT_PREVIEW_SANDBOX, HTML_SCRIPT_PREVIEW_CSP } from './html-document'
import { renderStaticHtml } from './static-html'

const page = (body: string) => `<!doctype html><html><head></head><body>${body}</body></html>`
const parse = (source: string) => new DOMParser().parseFromString(source, 'text/html')
const menu = page(`<h1>极简点餐</h1><main id="menu"></main><footer>购物车 0</footer><script>
window.__htmlPreviewRan = true;
document.querySelector('#menu').innerHTML = ['宫保鸡丁','鱼香肉丝','麻婆豆腐','清炒时蔬'].map(name => '<article class="dish">'+name+'</article>').join('');
</script>`)

describe('prepareHtmlPreview', () => {
  it('keeps existing static sanitization and uses no message channel', () => {
    const source = page('<style>.dish{color:red}</style><article class="dish">宫保鸡丁</article>')
    const old = renderStaticHtml(source)
    expect(old.ok).toBe(true)
    expect(prepareHtmlPreview(source)).toEqual({ ...old, mode: 'static', channel: null })
  })

  it.each(['application/json', 'application/ld+json', 'importmap', 'speculationrules'])('does not execute a %s data script', (type) => {
    const result = prepareHtmlPreview(page(`<h1>数据</h1><script type="${type}">{"a":1}</script>`))
    expect(result.ok && result.mode).toBe('static')
    expect(result.ok && parse(result.srcdoc).querySelector('script')).toBeNull()
  })

  it.each(['', 'module', 'text/javascript', 'application/javascript; charset=utf-8'])('preserves executable inline scripts of type %s', (type) => {
    const result = prepareHtmlPreview(page(`<h1>页面</h1><script type="${type}">window.rendered=true</script>`))
    expect(result.ok && result.mode).toBe('scripted')
    expect(result.ok && result.srcdoc).toContain('window.rendered=true')
  })

  it('retains script-built menu contents without running code in the parent', async () => {
    const result = prepareHtmlPreview(menu)
    if (!result.ok) throw new Error(result.reason)
    expect(result.mode).toBe('scripted')
    expect((window as Window & { __htmlPreviewRan?: boolean }).__htmlPreviewRan).toBeUndefined()
    const isolated = new JSDOM(result.srcdoc, { runScripts: 'dangerously', virtualConsole: new VirtualConsole() })
    try {
      await new Promise((resolve) => isolated.window.setTimeout(resolve, 0))
      expect(isolated.window.document.querySelectorAll('.dish')).toHaveLength(4)
      expect(isolated.window.document.body.textContent).toContain('宫保鸡丁')
      expect((window as Window & { __htmlPreviewRan?: boolean }).__htmlPreviewRan).toBeUndefined()
    } finally {
      isolated.window.close()
    }
  })

  it('preserves root/body styles, templates and script lookup dependencies', () => {
    const source = '<html class="dark" style="--accent:red"><head><style>.dish{display:flex}</style></head><body class="app" style="margin:0"><template id="dish"><article class="dish">菜</article></template><script>document.body.append(document.querySelector("#dish").content.cloneNode(true))</script></body></html>'
    const result = prepareHtmlPreview(source)
    if (!result.ok) throw new Error(result.reason)
    const doc = parse(result.srcdoc)
    expect(doc.documentElement.className).toBe('dark')
    expect(doc.documentElement.getAttribute('style')).toBe('--accent:red')
    expect(doc.body.className).toBe('app')
    expect(doc.body.getAttribute('style')).toBe('margin:0')
    expect(doc.querySelector('template')?.content.querySelector('.dish')).toBeTruthy()
    expect(doc.querySelector('style')?.textContent).toContain('.dish{display:flex}')
  })

  it('removes external dependencies and navigation while counting only blocked scripts', () => {
    const source = page('<script src="https://cdn.invalid/x.js"></script><script>window.ready=true</script><link rel="stylesheet" href="https://cdn.invalid/x.css"><meta http-equiv="refresh" content="0;url=https://example.invalid"><base href="https://example.invalid"><iframe src="https://example.invalid"></iframe><object data="x"></object><embed src="x"><a href="https://example.invalid" target="_top">链接</a><form action="https://example.invalid"><button formaction="https://example.invalid">提交</button></form>')
    const result = prepareHtmlPreview(source)
    if (!result.ok) throw new Error(result.reason)
    const doc = parse(result.srcdoc)
    expect(result.blocked).toEqual({ scripts: 1, externalStyles: 1 })
    expect(doc.querySelector('script[src],link,base,iframe,object,embed,meta[http-equiv="refresh"]')).toBeNull()
    expect(doc.querySelector('a')?.hasAttribute('href')).toBe(false)
    expect(doc.querySelector('[action],[formaction],[target]')).toBeNull()
    expect(result.srcdoc).toContain('window.ready=true')
  })

  it('places the fixed CSP first and never grants parent origin or external script access', () => {
    const result = prepareHtmlPreview('<html><head><meta http-equiv="Content-Security-Policy" content="default-src *;script-src *"><script>window.ready=true</script></head><body>页面</body></html>')
    if (!result.ok) throw new Error(result.reason)
    const doc = parse(result.srcdoc)
    expect(doc.head.firstElementChild?.getAttribute('http-equiv')).toBe('Content-Security-Policy')
    expect(doc.querySelectorAll('meta[http-equiv="Content-Security-Policy"]')).toHaveLength(1)
    expect(doc.head.firstElementChild?.getAttribute('content')).toBe(HTML_SCRIPT_PREVIEW_CSP)
    for (const directive of ["default-src 'none'", "script-src 'unsafe-inline'", "connect-src 'none'", "object-src 'none'", "frame-src 'none'", "base-uri 'none'", "form-action 'none'", "font-src 'none'", "media-src 'none'", "style-src 'unsafe-inline'", 'img-src data:']) expect(HTML_SCRIPT_PREVIEW_CSP).toContain(directive)
    expect(HTML_SCRIPT_PREVIEW_SANDBOX.split(/\s+/)).toEqual(['allow-scripts'])
    expect(result.srcdoc).not.toContain('script-src *')
  })

  it('creates a distinct nonempty channel per scripted document', () => {
    const first = prepareHtmlPreview(menu)
    const second = prepareHtmlPreview(menu)
    if (!first.ok || !second.ok) throw new Error('expected script documents')
    expect(first.channel).toBeTruthy()
    expect(second.channel).not.toBe(first.channel)
    expect(readHtmlPreviewMessage({ type: 'inline-html-preview', channel: first.channel, height: 300 }, second.channel!)).toBeNull()
  })

  it('keeps existing size and empty-document failures', () => {
    expect(prepareHtmlPreview(page(''))).toEqual({ ok: false, reason: 'empty' })
    expect(prepareHtmlPreview('x'.repeat(256 * 1024 + 1))).toEqual({ ok: false, reason: 'too-large' })
    expect(prepareHtmlPreview(page('<script src="https://cdn.invalid/app.js"></script>'))).toEqual({ ok: false, reason: 'needs-script' })
  })

  it('runs bootstrap before generated scripts and posts only height, wheel and safe error messages', async () => {
    const result = prepareHtmlPreview(menu)
    if (!result.ok || !result.channel) throw new Error('expected script document')
    const messages: unknown[] = []
    const isolated = new JSDOM(result.srcdoc, {
      runScripts: 'dangerously', virtualConsole: new VirtualConsole(),
      beforeParse(win: any) {
        Object.defineProperty(win, 'parent', { value: { postMessage: (data: unknown) => messages.push(data) } })
      },
    })
    try {
      await new Promise((resolve) => isolated.window.setTimeout(resolve, 10))
      expect(messages.some((data) => {
        const message = readHtmlPreviewMessage(data, result.channel!)
        return message !== null && 'height' in message
      })).toBe(true)
      messages.length = 0
      const wheel = new isolated.window.WheelEvent('wheel', { ctrlKey: true, deltaY: -100, cancelable: true })
      isolated.window.document.dispatchEvent(wheel)
      expect(wheel.defaultPrevented).toBe(true)
      expect(messages.map((data) => readHtmlPreviewMessage(data, result.channel!))).toContainEqual({ zoom: 1 })
      messages.length = 0
      isolated.window.document.dispatchEvent(new isolated.window.WheelEvent('wheel', { deltaY: 100, cancelable: true }))
      expect(messages).toHaveLength(0)
      isolated.window.dispatchEvent(new isolated.window.ErrorEvent('error', { message: '<script>secret</script>' }))
      isolated.window.dispatchEvent(new isolated.window.Event('unhandledrejection'))
      const violation = new isolated.window.Event('securitypolicyviolation')
      Object.defineProperty(violation, 'violatedDirective', { value: 'connect-src' })
      isolated.window.document.dispatchEvent(violation)
      expect(messages).toHaveLength(3)
      for (const message of messages) {
        const safe = readHtmlPreviewMessage(message, result.channel!)
        expect(safe && 'error' in safe && safe.error.length).toBeGreaterThan(0)
        expect(JSON.stringify(message)).not.toContain('secret')
        expect(Object.keys(message as object).sort()).toEqual(['channel', 'error', 'type'])
      }
    } finally {
      isolated.window.close()
    }
  })

  it.each(['childList', 'class'])('reports content overflow after %s changes even if the body box never resizes', async (change) => {
    const action = change === 'childList'
      ? 'document.body.appendChild(document.createElement("article"))'
      : 'document.body.className="expanded"'
    const result = prepareHtmlPreview(page(`<main>标题</main><script>setTimeout(() => { ${action} }, 15)</script>`))
    if (!result.ok || !result.channel) throw new Error('expected script document')
    const messages: unknown[] = []
    const isolated = new JSDOM(result.srcdoc, {
      runScripts: 'dangerously', virtualConsole: new VirtualConsole(),
      beforeParse(win: any) {
        Object.defineProperty(win, 'parent', { value: { postMessage: (data: unknown) => messages.push(data) } })
        // jsdom 没有布局：用受控 scrollHeight 表示溢出，body 的边界盒始终不变。
        Object.defineProperty(win.HTMLElement.prototype, 'scrollHeight', {
          get() { return this.querySelector('article') || this.className === 'expanded' ? 420 : 100 },
        })
      },
    })
    try {
      await new Promise((resolve) => isolated.window.setTimeout(resolve, 50))
      const heights = messages.map((data) => readHtmlPreviewMessage(data, result.channel!))
      expect(heights).toContainEqual({ height: 100 })
      expect(heights).toContainEqual({ height: 420 })
      expect(heights.filter((message) => message && 'height' in message)).toHaveLength(2)
    } finally {
      isolated.window.close()
    }
  })
})

describe('readHtmlPreviewMessage', () => {
  const message = (fields: Record<string, unknown>) => ({ type: 'inline-html-preview', channel: 'local-channel', ...fields })

  it.each([1, 2400, 1_000_000])('accepts bounded height %s', (height) => {
    expect(readHtmlPreviewMessage(message({ height }), 'local-channel')).toEqual({ height })
  })
  it.each([1, -1])('accepts zoom direction %s without forwarding other instructions', (zoom) => {
    expect(readHtmlPreviewMessage(message({ zoom, command: 'never execute' }), 'local-channel')).toEqual({ zoom })
  })
  it('accepts a short error without forwarding extra fields', () => {
    expect(readHtmlPreviewMessage(message({ error: ' 页面脚本执行失败 ', command: 'never execute' }), 'local-channel')).toEqual({ error: '页面脚本执行失败' })
  })
  it.each([0, -1, NaN, Infinity, -Infinity, 1_000_001, '300', null])('rejects invalid height %s', (height) => {
    expect(readHtmlPreviewMessage(message({ height }), 'local-channel')).toBeNull()
  })
  it.each([null, [], {}, { type: 'other', channel: 'local-channel', height: 10 }, { type: 'inline-html-preview', channel: 'other', height: 10 }, { zoom: 0 }, { zoom: 2 }, { error: '' }, { error: ' ' }, { error: false }, { error: 'x'.repeat(301) }, { height: 1, zoom: 1 }])('rejects malformed or ambiguous message %#', (data) => {
    const wrapped = data && typeof data === 'object' && !Array.isArray(data) ? message(data as Record<string, unknown>) : data
    expect(readHtmlPreviewMessage(wrapped, 'local-channel')).toBeNull()
  })
})
