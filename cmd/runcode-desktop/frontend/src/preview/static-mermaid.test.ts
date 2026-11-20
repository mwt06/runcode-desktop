// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest'
import {
  checkMermaidSource, countFlowNodes, createMermaidRenderer, createSerialQueue, MERMAID_CONFIG,
  mermaidRuntimeSupported, type MermaidApi,
} from './static-mermaid'
import { PREVIEW_LIMITS } from './static-detect'

describe('checkMermaidSource', () => {
  it('accepts the supported diagram types, including Chinese labels', () => {
    for (const head of ['flowchart TD', 'graph LR', 'sequenceDiagram', 'classDiagram', 'classDiagram-v2', 'stateDiagram-v2', 'erDiagram']) {
      expect(checkMermaidSource(`${head}\n  A[客户端] --> B[网关]`)).toEqual({ ok: true, diagram: head.split(' ')[0] })
    }
  })
  it('skips leading comments and blank lines when finding the type', () => {
    expect(checkMermaidSource('\n%% 架构图\n\nflowchart TD\nA-->B')).toEqual({ ok: true, diagram: 'flowchart' })
  })
  it('accepts chart and common diagram types used in real replies', () => {
    for (const head of [
      'pie title Share',
      'xychart-beta',
      'xychart',
      'quadrantChart',
      'gantt',
      'gitGraph',
      'journey',
      'timeline',
      'mindmap',
      'sankey-beta',
      'sankey',
      'block-beta',
      'architecture-beta',
      'requirementDiagram',
      'stateDiagram',
    ]) {
      expect(checkMermaidSource(`${head}\n  a`)).toEqual({ ok: true, diagram: head.split(/[\s\n]/)[0] })
    }
  })
  it('still rejects unknown or unsafe diagram heads', () => {
    for (const head of ['C4Context', 'unknownDiagram', 'packet-beta']) {
      expect(checkMermaidSource(`${head}\n  a`)).toEqual({ ok: false, reason: 'unsupported-type' })
    }
  })
  it('refuses init directives and frontmatter config (X04)', () => {
    expect(checkMermaidSource("%%{init: {'securityLevel':'loose'}}%%\nflowchart TD\nA-->B")).toEqual({ ok: false, reason: 'directive' })
    expect(checkMermaidSource('flowchart TD\nA-->B\n%%{ wrap }%%')).toEqual({ ok: false, reason: 'directive' })
    expect(checkMermaidSource('---\nconfig:\n  securityLevel: loose\n---\nflowchart TD\nA-->B')).toEqual({ ok: false, reason: 'frontmatter' })
  })
  it('refuses click / callback / link bindings (X04)', () => {
    expect(checkMermaidSource('flowchart TD\nA-->B\nclick A call alert()')).toEqual({ ok: false, reason: 'interaction' })
    expect(checkMermaidSource('flowchart TD\nA-->B; click B href "https://x"')).toEqual({ ok: false, reason: 'interaction' })
    expect(checkMermaidSource('classDiagram\nclass A\ncallback A "fn"')).toEqual({ ok: false, reason: 'interaction' })
    expect(checkMermaidSource('classDiagram\nclass A\nlink A "https://x"')).toEqual({ ok: false, reason: 'interaction' })
    expect(checkMermaidSource('sequenceDiagram\nlink Alice: Dash @ https://x')).toEqual({ ok: false, reason: 'interaction' })
  })
  it('does not mistake a node named link/click for a binding', () => {
    expect(checkMermaidSource('flowchart TD\nlink --> click\nclick-->B')).toEqual({ ok: true, diagram: 'flowchart' })
  })
  it('enforces size, statement, edge and node budgets (P01)', () => {
    expect(checkMermaidSource('flowchart TD\n' + 'A'.repeat(PREVIEW_LIMITS.mermaidBytes))).toEqual({ ok: false, reason: 'too-large' })
    const nodes = Array.from({ length: PREVIEW_LIMITS.mermaidNodes + 1 }, (_, i) => `N${i}[节点${i}]`).join('\n')
    expect(checkMermaidSource(`flowchart TD\n${nodes}`)).toEqual({ ok: false, reason: 'too-complex' })
    const edges = Array.from({ length: 101 }, () => 'A-->B-->C-->D-->E').join('\n')
    expect(checkMermaidSource(`flowchart TD\n${edges}`)).toEqual({ ok: false, reason: 'too-complex' })
    const stmts = Array.from({ length: PREVIEW_LIMITS.mermaidStatements + 1 }, () => 'Alice->>Bob: hi').join('\n')
    expect(checkMermaidSource(`sequenceDiagram\n${stmts}`)).toEqual({ ok: false, reason: 'too-complex' })
  })
  it('rejects empty input', () => {
    expect(checkMermaidSource('  \n')).toEqual({ ok: false, reason: 'empty' })
    expect(checkMermaidSource('%% only a comment')).toEqual({ ok: false, reason: 'empty' })
  })
})

describe('countFlowNodes', () => {
  it('counts distinct ids across chains, labels and & groups, ignoring keywords', () => {
    expect(countFlowNodes(['A[开始] -->|是| B{判断}', 'B --> C & D', 'subgraph S', 'C -.-> A', 'end', 'style A fill:#f9f'])).toBe(4)
  })
})

describe('createSerialQueue', () => {
  it('runs tasks one at a time in order, even when one fails', async () => {
    const enqueue = createSerialQueue()
    const log: string[] = []
    let active = 0
    const task = (name: string, fail = false) => async () => {
      active++
      expect(active).toBe(1)
      log.push(`start ${name}`)
      await new Promise((r) => setTimeout(r, 5))
      log.push(`end ${name}`)
      active--
      if (fail) throw new Error(name)
      return name
    }
    const results = await Promise.allSettled([enqueue(task('a'))!, enqueue(task('b', true))!, enqueue(task('c'))!])
    expect(results.map((r) => r.status)).toEqual(['fulfilled', 'rejected', 'fulfilled'])
    expect(log).toEqual(['start a', 'end a', 'start b', 'end b', 'start c', 'end c'])
  })
  it('refuses work beyond the pending bound', async () => {
    const enqueue = createSerialQueue(2)
    const slow = () => new Promise<void>((r) => setTimeout(r, 5))
    const a = enqueue(slow)
    const b = enqueue(slow)
    expect(enqueue(slow)).toBeNull()
    await Promise.all([a, b])
    expect(enqueue(slow)).not.toBeNull()
  })
})

function fakeMermaid(svg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 50"><rect width="10" height="10"/><text>节点</text></svg>') {
  const calls: string[] = []
  const api: MermaidApi & { calls: string[] } = {
    calls,
    initialize: vi.fn(),
    parse: vi.fn(async (text: string) => {
      if (text.includes('BROKEN')) throw new Error('Parse error')
      return true
    }),
    render: vi.fn(async (id: string, _text: string, box?: Element) => {
      calls.push(id)
      // 模拟 Mermaid 往文档里留测量节点与错误节点。
      const tmp = document.createElement('div')
      tmp.id = 'd' + id
      ;(box ?? document.body).appendChild(tmp)
      const stray = document.createElement('svg')
      stray.id = id
      document.body.appendChild(stray)
      expect(box?.isConnected).toBe(true)
      expect((box as HTMLElement).style.display).not.toBe('none')
      return { svg: svg.replace('<svg ', `<svg id="${id}" `) }
    }),
  }
  return api
}

describe('createMermaidRenderer', () => {
  it('renders, sanitizes and cleans up temporary DOM (F02)', async () => {
    const api = fakeMermaid()
    const render = createMermaidRenderer(async () => api)
    const r = await render('flowchart TD\nA[客户端]-->B[网关]')
    if (!r.ok) throw new Error(r.reason)
    expect(r.svg).toContain('节点')
    expect([r.width, r.height]).toEqual([100, 50])
    expect(document.body.children).toHaveLength(0)
  })
  it('initializes once with the fixed strict configuration', async () => {
    const api = fakeMermaid()
    const render = createMermaidRenderer(async () => api)
    await render('graph TD\nA-->B')
    await render('graph TD\nB-->C')
    expect(api.initialize).toHaveBeenCalledTimes(1)
    const cfg = vi.mocked(api.initialize).mock.calls[0][0]
    expect(cfg).toMatchObject({ securityLevel: 'strict', startOnLoad: false, htmlLabels: false })
    expect(MERMAID_CONFIG.secure).toContain('securityLevel')
  })
  it('uses a unique id per render so repeated diagrams do not collide', async () => {
    const api = fakeMermaid()
    const render = createMermaidRenderer(async () => api)
    await Promise.all([render('graph TD\nA-->B'), render('graph TD\nA-->B'), render('graph TD\nA-->B')])
    expect(new Set(api.calls).size).toBe(3)
  })
  it('does not load Mermaid for input rejected by the pre-check', async () => {
    const load = vi.fn(async () => fakeMermaid())
    const render = createMermaidRenderer(load)
    expect(await render('C4Context\nSystem_Boundary')).toEqual({ ok: false, reason: 'unsupported-type' })
    expect(load).not.toHaveBeenCalled()
  })
  it('refuses to load when CSSStyleSheet is unavailable (Kylin WebKit)', async () => {
    const Original = globalThis.CSSStyleSheet
    // @ts-expect-error deliberate probe
    globalThis.CSSStyleSheet = undefined
    expect(mermaidRuntimeSupported()).toBe(false)
    const load = vi.fn(async () => fakeMermaid())
    const render = createMermaidRenderer(load)
    expect(await render('flowchart TD\nA-->B')).toEqual({ ok: false, reason: 'platform-unsupported' })
    expect(load).not.toHaveBeenCalled()
    globalThis.CSSStyleSheet = Original
  })
  it('falls back on syntax errors and still cleans up (F08)', async () => {
    const render = createMermaidRenderer(async () => fakeMermaid())
    expect(await render('flowchart TD\nBROKEN -->')).toEqual({ ok: false, reason: 'syntax' })
    expect(document.body.children).toHaveLength(0)
  })
  it('strips active content from Mermaid output and fails when nothing drawable remains', async () => {
    const evil = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><script>alert(1)</script><foreignObject><div>x</div></foreignObject><rect width="1" height="1" onclick="x()"/></svg>'
    const r = await createMermaidRenderer(async () => fakeMermaid(evil))('graph TD\nA-->B')
    expect(r.ok && r.svg).not.toMatch(/script|foreignObject|onclick/)
    const empty = '<svg xmlns="http://www.w3.org/2000/svg"><script>x</script></svg>'
    expect(await createMermaidRenderer(async () => fakeMermaid(empty))('graph TD\nA-->B')).toEqual({ ok: false, reason: 'unsafe-output' })
  })
  it('reports load failures and retries the import next time', async () => {
    let attempts = 0
    const load = vi.fn(async () => {
      if (++attempts === 1) throw new Error('chunk failed')
      return fakeMermaid()
    })
    const render = createMermaidRenderer(load)
    expect(await render('graph TD\nA-->B')).toEqual({ ok: false, reason: 'load-failed' })
    expect((await render('graph TD\nA-->B')).ok).toBe(true)
    expect(load).toHaveBeenCalledTimes(2)
  })
  it('honors cancellation before and after rendering (S03)', async () => {
    const api = fakeMermaid()
    const render = createMermaidRenderer(async () => api)
    const before = new AbortController()
    before.abort()
    expect(await render('graph TD\nA-->B', before.signal)).toEqual({ ok: false, reason: 'cancelled' })
    expect(api.render).not.toHaveBeenCalled()

    const during = new AbortController()
    vi.mocked(api.render).mockImplementationOnce(async (id) => { during.abort(); return { svg: `<svg id="${id}" viewBox="0 0 1 1"><rect width="1" height="1"/></svg>` } })
    expect(await render('graph TD\nA-->B', during.signal)).toEqual({ ok: false, reason: 'cancelled' })
    expect(document.body.children).toHaveLength(0)
  })
  it('reports busy instead of queueing without bound', async () => {
    let release: () => void = () => {}
    const gate = new Promise<void>((r) => { release = r })
    const api = fakeMermaid()
    vi.mocked(api.render).mockImplementation(async (id) => { await gate; return { svg: `<svg id="${id}" viewBox="0 0 1 1"><rect width="1" height="1"/></svg>` } })
    const render = createMermaidRenderer(async () => api, { maxPending: 1 })
    const first = render('graph TD\nA-->B')
    expect(await render('graph TD\nA-->B')).toEqual({ ok: false, reason: 'busy' })
    release()
    expect((await first).ok).toBe(true)
  })
  it('reports timeout when a queued render never settles', async () => {
    let release: () => void = () => {}
    const gate = new Promise<void>((resolve) => { release = resolve })
    const api = fakeMermaid()
    vi.mocked(api.render).mockImplementation(async (id) => {
      await gate
      return { svg: `<svg id="${id}" viewBox="0 0 1 1"><rect width="1" height="1"/></svg>` }
    })
    const render = createMermaidRenderer(async () => api, { timeoutMs: 5 })
    const pending = render('graph TD\nA-->B')
    const outcome = await Promise.race([
      pending,
      new Promise<string>((resolve) => setTimeout(() => resolve('still-pending'), 25)),
    ])
    expect(outcome).toEqual({ ok: false, reason: 'timeout' })

    release()
    await pending
    await vi.waitFor(() => expect(document.body.children).toHaveLength(0))
  })
})
