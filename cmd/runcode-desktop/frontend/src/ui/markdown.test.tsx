import { describe, it, expect } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { Markdown } from './markdown'

describe('Markdown renderCodeBlock slot', () => {
  it('leaves ordinary markdown alone when no slot is provided', () => {
    const html = renderToStaticMarkup(<Markdown>{'hello **x**'}</Markdown>)
    expect(html).toContain('<strong')
    expect(html).toContain('hello')
  })
  it('passes lang/meta/value/offsets from the AST, not from highlighted React children', () => {
    const seen: { lang: string; meta: string; value: string; start: number; end: number }[] = []
    const md = 'intro\n\n```html preview\n<p>hi</p>\n```\n'
    renderToStaticMarkup(
      <Markdown
        renderCodeBlock={(info) => {
          seen.push({ lang: info.lang, meta: info.meta, value: info.value, start: info.start, end: info.end })
          return <div data-preview={info.lang}>{info.value}</div>
        }}
      >
        {md}
      </Markdown>,
    )
    expect(seen).toHaveLength(1)
    expect(seen[0]).toMatchObject({ lang: 'html', meta: 'preview', value: '<p>hi</p>' })
    expect(md.slice(seen[0].start, seen[0].end)).toContain('```html preview')
  })
  it('falls back to the default code block when the slot returns null', () => {
    const html = renderToStaticMarkup(
      <Markdown renderCodeBlock={() => null}>{'```js\nconst x = 1\n```'}</Markdown>,
    )
    expect(html).toContain('language-js')
  })
  it('does not invoke the slot in textOnly mode', () => {
    let called = 0
    renderToStaticMarkup(
      <Markdown textOnly renderCodeBlock={() => { called++; return <div /> }}>{'```svg\n<svg></svg>\n```'}</Markdown>,
    )
    expect(called).toBe(0)
  })
  it('suppresses unresolved local image syntax instead of rendering a broken image', () => {
    const html = renderToStaticMarkup(
      <Markdown>{'![一张真实的篮球](image_e5e4640f.jpg)'}</Markdown>,
    )
    expect(html).not.toContain('<img')
    expect(html).not.toContain('一张真实的篮球')
  })
})
