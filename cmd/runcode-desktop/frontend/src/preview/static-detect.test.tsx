import { describe, it, expect } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {
  classifyCodeBlock, contentDigest, fenceClosed, planMessagePreviews, previewCacheKey, PREVIEW_LIMITS,
} from './static-detect'

// 用真实的 react-markdown 解析拿到 <pre> 节点的原文位置，证明 fenceClosed 与解析器
// 给出的偏移量对得上，而不是只对手写的偏移量成立。
function preRanges(md: string): { start: number; end: number; lang?: string; meta?: string; value: string }[] {
  const out: { start: number; end: number; lang?: string; meta?: string; value: string }[] = []
  renderToStaticMarkup(
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        pre: ({ node }) => {
          const code = node?.children[0]
          const pos = node?.position
          if (pos && code && code.type === 'element') {
            const cls = (code.properties.className as string[] | undefined)?.[0]
            const text = code.children.map((c) => (c.type === 'text' ? c.value : '')).join('')
            out.push({
              start: pos.start.offset!, end: pos.end.offset!,
              lang: cls?.replace(/^language-/, ''),
              meta: (code.data as { meta?: string } | undefined)?.meta,
              value: text,
            })
          }
          return null
        },
      }}
    >
      {md}
    </ReactMarkdown>,
  )
  return out
}

function closedOf(md: string): boolean[] {
  return preRanges(md).map((r) => fenceClosed(md, r.start, r.end))
}

describe('fenceClosed (with real parser offsets)', () => {
  it('accepts a normal closed backtick fence', () => {
    expect(closedOf('说明\n\n```mermaid\ngraph TD\nA-->B\n```\n\n后文')).toEqual([true])
  })
  it('rejects a fence still being streamed (runs to end of document)', () => {
    expect(closedOf('说明\n\n```svg\n<svg>\n<rect/>')).toEqual([false])
  })
  it('rejects an opening fence with no body yet', () => {
    expect(closedOf('```html')).toEqual([false])
  })
  it('supports tilde fences and longer fences', () => {
    expect(closedOf('~~~svg\n<svg></svg>\n~~~')).toEqual([true])
    expect(closedOf('````mermaid\ngraph TD\n```\nstill code\n````')).toEqual([true])
  })
  it('does not let a shorter or different fence close it', () => {
    expect(closedOf('````svg\n<svg></svg>\n```')).toEqual([false])
    expect(closedOf('~~~svg\n<svg></svg>\n```')).toEqual([false])
  })
  it('handles fences nested in list items and blockquotes', () => {
    expect(closedOf('- 图如下\n\n  ```mermaid\n  graph TD\n  A-->B\n  ```\n- 下一项')).toEqual([true])
    expect(closedOf('> ```svg\n> <svg></svg>\n> ```')).toEqual([true])
  })
  it('treats an unclosed fence that ends with its container as incomplete', () => {
    expect(closedOf('> ```svg\n> <svg></svg>\n\n正文')).toEqual([false])
  })
  it('handles CRLF line endings', () => {
    expect(closedOf('```svg\r\n<svg></svg>\r\n```\r\n')).toEqual([true])
  })
  it('ignores indented code blocks (no fence)', () => {
    expect(closedOf('段落\n\n    <svg></svg>\n\n后文')).toEqual([false])
  })
  it('rejects bad ranges', () => {
    expect(fenceClosed('```\na\n```', 5, 2)).toBe(false)
    expect(fenceClosed('```\na\n```', 0, 999)).toBe(false)
  })
  it('exposes lang/meta/value from the parser, not from highlighted children', () => {
    const [r] = preRanges('```html preview\n<p>hi</p>\n```')
    expect(r).toMatchObject({ lang: 'html', meta: 'preview', value: '<p>hi</p>\n' })
  })
})

describe('classifyCodeBlock', () => {
  const svg = '<svg viewBox="0 0 10 10"><rect width="5" height="5"/></svg>'
  const page = '<!doctype html><html><body><div>架构</div></body></html>'

  it('previews closed mermaid / svg / complete html documents', () => {
    expect(classifyCodeBlock({ lang: 'mermaid', value: 'graph TD\nA-->B', closed: true })).toEqual({ kind: 'mermaid' })
    expect(classifyCodeBlock({ lang: 'svg', value: svg, closed: true })).toEqual({ kind: 'svg' })
    expect(classifyCodeBlock({ lang: 'SVG', value: `<?xml version="1.0"?>\n<!-- c -->\n${svg}\n`, closed: true })).toEqual({ kind: 'svg' })
    for (const lang of ['html', 'htm', 'h5']) {
      expect(classifyCodeBlock({ lang, value: page, closed: true })).toEqual({ kind: 'html' })
    }
  })
  it('keeps html fragments as code unless marked preview', () => {
    expect(classifyCodeBlock({ lang: 'html', value: '<button>OK</button>', closed: true })).toEqual({ kind: null, reason: 'html-fragment' })
    expect(classifyCodeBlock({ lang: 'html', meta: 'preview', value: '<button>OK</button>', closed: true })).toEqual({ kind: 'html' })
  })
  it('honors the source marker', () => {
    expect(classifyCodeBlock({ lang: 'svg', meta: 'source', value: svg, closed: true })).toEqual({ kind: null, reason: 'source' })
    expect(classifyCodeBlock({ lang: 'html', meta: 'preview source', value: page, closed: true })).toEqual({ kind: null, reason: 'source' })
  })
  it('recognizes svg/mermaid aliases and safe unlabeled markup', () => {
    expect(classifyCodeBlock({ lang: 'xml', value: svg, closed: true })).toEqual({ kind: 'svg' })
    expect(classifyCodeBlock({ lang: 'svg+xml', value: svg, closed: true })).toEqual({ kind: 'svg' })
    expect(classifyCodeBlock({ lang: 'mmd', value: 'graph TD\nA-->B', closed: true })).toEqual({ kind: 'mermaid' })
    expect(classifyCodeBlock({ value: svg, closed: true })).toEqual({ kind: 'svg' })
    expect(classifyCodeBlock({ value: page, closed: true })).toEqual({ kind: 'html' })
    expect(classifyCodeBlock({ value: 'const x = 1', closed: true })).toEqual({ kind: null, reason: 'no-language' })
    expect(classifyCodeBlock({ lang: 'python', value: 'print(1)', closed: true })).toEqual({ kind: null, reason: 'language' })
  })
  it('previews html fences that wrap a static svg fragment', () => {
    expect(classifyCodeBlock({ lang: 'html', value: svg, closed: true })).toEqual({ kind: 'svg' })
    expect(classifyCodeBlock({ lang: 'html', value: `<div>${svg}</div>`, closed: true })).toEqual({ kind: 'html' })
  })
  it('refuses unclosed, empty, non-svg and oversized input', () => {
    expect(classifyCodeBlock({ lang: 'svg', value: svg, closed: false })).toEqual({ kind: null, reason: 'unclosed' })
    expect(classifyCodeBlock({ lang: 'mermaid', value: '  \n', closed: true })).toEqual({ kind: null, reason: 'empty' })
    expect(classifyCodeBlock({ lang: 'svg', value: '<div></div>', closed: true })).toEqual({ kind: null, reason: 'not-svg' })
    expect(classifyCodeBlock({ lang: 'svg', value: '<svg><rect/>', closed: true })).toEqual({ kind: null, reason: 'not-svg' })
    const big = 'graph TD\n' + 'A-->B\n'.repeat(PREVIEW_LIMITS.mermaidBytes / 6 + 1)
    expect(classifyCodeBlock({ lang: 'mermaid', value: big, closed: true })).toEqual({ kind: null, reason: 'too-large' })
  })
  it('counts the budget in UTF-8 bytes (Chinese labels are 3 bytes each)', () => {
    const zh = '节'.repeat(Math.floor(PREVIEW_LIMITS.mermaidBytes / 3) + 1)
    expect(classifyCodeBlock({ lang: 'mermaid', value: `graph TD\nA[${zh}]`, closed: true })).toEqual({ kind: null, reason: 'too-large' })
  })
})

describe('contentDigest / previewCacheKey', () => {
  it('is stable and distinguishes content', () => {
    expect(contentDigest('graph TD')).toBe(contentDigest('graph TD'))
    expect(contentDigest('graph TD')).not.toBe(contentDigest('graph LR'))
    expect(contentDigest('')).toMatch(/^[0-9a-f]{16}-0$/)
  })
  it('includes session, message, kind and renderer version in the key', () => {
    const base = { sessionId: 's1', messageId: 'm1', kind: 'svg' as const, digest: 'd' }
    expect(previewCacheKey(base)).not.toBe(previewCacheKey({ ...base, sessionId: 's2' }))
    expect(previewCacheKey(base)).not.toBe(previewCacheKey({ ...base, kind: 'html' }))
    expect(previewCacheKey(base)).toMatch(/^1\|/)
  })
})

describe('planMessagePreviews', () => {
  it('keeps order, dedupes identical content of the same kind, and caps the count', () => {
    const c = (id: string, digest: string, kind: 'svg' | 'html' | 'mermaid' = 'svg') => ({ id, kind, digest })
    const chosen = planMessagePreviews([c('a', 'x'), c('b', 'x'), c('c', 'x', 'html'), c('d', 'y'), c('e', 'z')], 3)
    expect([...chosen]).toEqual(['a', 'c', 'd'])
  })
  it('defaults to the per-message limit', () => {
    const many = Array.from({ length: 10 }, (_, i) => ({ id: String(i), kind: 'svg' as const, digest: String(i) }))
    expect(planMessagePreviews(many).size).toBe(PREVIEW_LIMITS.perMessage)
  })
})
