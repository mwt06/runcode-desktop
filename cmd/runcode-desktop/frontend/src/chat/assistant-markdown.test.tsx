import { describe, it, expect } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { listFenceBlocks, relatedPathsForKind, selectPreviewBlocks, AssistantMarkdown } from './assistant-markdown'

const SVG = '<svg viewBox="0 0 10 10"><rect width="5" height="5"/></svg>'
const PAGE = '<!doctype html><html><body><div>架构</div></body></html>'

describe('listFenceBlocks / selectPreviewBlocks', () => {
  it('selects closed svg / mermaid / complete html and html preview fragments', () => {
    const md = [
      '说明',
      '',
      '```svg',
      SVG,
      '```',
      '',
      '```mermaid',
      'flowchart TD',
      'A-->B',
      '```',
      '',
      '```html',
      PAGE,
      '```',
      '',
      '```html preview',
      '<div>片段</div>',
      '```',
    ].join('\n')
    const selected = selectPreviewBlocks(md, false)
    expect(selected.size).toBe(4)
    expect([...selected.values()]).toEqual(['svg', 'mermaid', 'html', 'html'])
  })
  it('keeps source-marked and html fragments as code', () => {
    const md = '```svg source\n' + SVG + '\n```\n\n```html\n<button>x</button>\n```'
    expect(selectPreviewBlocks(md, false).size).toBe(0)
  })
  it('selects closed fences while streaming', () => {
    const md = '```svg\n' + SVG + '\n```'
    expect(selectPreviewBlocks(md, true).size).toBe(1)
    expect(listFenceBlocks(md)).toHaveLength(1)
  })
  it('rejects an unclosed fence that the parser still yields as a code node', () => {
    const md = '```svg\n<svg><rect/>'
    const [b] = listFenceBlocks(md)
    expect(b).toBeTruthy()
    expect(selectPreviewBlocks(md, false).size).toBe(0)
  })
  it('dedupes identical content and caps at six', () => {
    const one = '```svg\n' + SVG + '\n```'
    const md = Array.from({ length: 8 }, () => one).join('\n\n')
    expect(selectPreviewBlocks(md, false).size).toBe(1)
    const many = Array.from({ length: 8 }, (_, i) => '```svg\n<svg viewBox="0 0 1 1"><rect width="1" height="1" id="n' + i + '"/></svg>\n```').join('\n\n')
    expect(selectPreviewBlocks(many, false).size).toBe(6)
  })
})

describe('relatedPathsForKind', () => {
  it('picks same-message static paths matching the preview kind', () => {
    const md = '已生成 charts/a.svg 与 notes/flow.mmd\n\n```svg\n' + SVG + '\n```'
    expect(relatedPathsForKind(md, 'svg')).toEqual(['charts/a.svg'])
    expect(relatedPathsForKind(md, 'mermaid')).toEqual(['notes/flow.mmd'])
    expect(relatedPathsForKind(md, 'html')).toEqual([])
  })
})

describe('AssistantMarkdown', () => {
  it('renders ordinary markdown unchanged when nothing is previewable', () => {
    const html = renderToStaticMarkup(<AssistantMarkdown messageId="m1">你好 **世界**</AssistantMarkdown>)
    expect(html).toContain('你好')
    expect(html).toContain('<strong')
    expect(html).not.toContain('静态预览')
  })
  it('does not enable preview in textOnly mode (knowledge answers)', () => {
    const md = '```svg\n' + SVG + '\n```'
    const html = renderToStaticMarkup(<AssistantMarkdown messageId="m1" textOnly>{md}</AssistantMarkdown>)
    expect(html).toContain('language-svg')
    expect(html).not.toContain('min-h-12') // loading placeholder from InlinePreview
  })
  it('keeps an unclosed streaming fence as source', () => {
    const md = '```svg\n' + SVG
    const html = renderToStaticMarkup(<AssistantMarkdown messageId="m1" streaming>{md}</AssistantMarkdown>)
    expect(html).toContain('language-svg')
    expect(html).not.toContain('min-h-12')
  })
  it('mounts an inline preview slot for a closed svg block (placeholder before effect)', () => {
    // renderToStaticMarkup 不跑 effect，所以看到的是加载占位而不是 <img>；证明插槽已接上。
    const md = '前面\n\n```svg\n' + SVG + '\n```\n\n后面'
    const html = renderToStaticMarkup(<AssistantMarkdown messageId="m1">{md}</AssistantMarkdown>)
    expect(html).toContain('前面')
    expect(html).toContain('后面')
    expect(html).toContain('min-h-12')
    expect(html).not.toContain('language-svg')
  })

  it('mounts a file preview for a path-only SVG message', () => {
    const html = renderToStaticMarkup(
      <AssistantMarkdown messageId="m1" sessionId="S" onOpenCodePreview={() => {}}>
        已生成 charts/a.svg
      </AssistantMarkdown>,
    )
    expect(html).toContain('data-file-preview-path="charts/a.svg"')
  })

  it('does not duplicate a fenced chart when the same file path is mentioned', () => {
    const md = `已生成 charts/a.svg\n\n\`\`\`svg\n${SVG}\n\`\`\``
    const html = renderToStaticMarkup(
      <AssistantMarkdown messageId="m1" sessionId="S" onOpenCodePreview={() => {}}>
        {md}
      </AssistantMarkdown>,
    )
    expect(html).not.toContain('data-file-preview-path="charts/a.svg"')
    expect(html).toContain('data-file-preview-link="charts/a.svg"')
  })
})
