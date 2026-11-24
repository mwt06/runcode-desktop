import { describe, expect, it } from 'vitest'
import { codePreviewTarget } from './code-preview'

const base = {
  source: '<svg/>',
  title: 'SVG 预览',
  sessionId: 'S',
  messageId: 'm1',
  previewId: '0:10',
}

describe('codePreviewTarget', () => {
  it('opens a unique SVG file in the right panel', () => {
    expect(codePreviewTarget({ ...base, kind: 'svg', relatedPaths: ['charts/a.svg'] })).toMatchObject({
      kind: 'file',
      relPath: 'charts/a.svg',
      ownerKey: 'file:S:charts/a.svg',
    })
  })

  it('keeps Mermaid rendering in memory even when an mmd file exists', () => {
    expect(codePreviewTarget({ ...base, kind: 'mermaid', relatedPaths: ['charts/flow.mmd'] })).toMatchObject({
      kind: 'inline-static',
      ownerKey: 'inline:S:m1:0:10',
      sourceKind: 'mermaid',
    })
  })

  it('uses an inline preview instead of guessing when multiple files match', () => {
    expect(codePreviewTarget({
      ...base,
      kind: 'html',
      relatedPaths: ['one/report.html', 'two/report.html'],
    })).toMatchObject({ kind: 'inline-static', sourceKind: 'html' })
  })
})
