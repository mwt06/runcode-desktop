import { describe, expect, it } from 'vitest'
import { filterContactRows, mergeContactEmails } from './contact-picker-modal'

describe('mergeContactEmails', () => {
  it('dedupes case-insensitively and keeps existing order', () => {
    expect(mergeContactEmails('a@ex.com, b@ex.com', ['B@ex.com', 'c@ex.com']))
      .toBe('a@ex.com, b@ex.com, c@ex.com')
  })

  it('handles empty existing', () => {
    expect(mergeContactEmails('', ['x@y.com'])).toBe('x@y.com')
  })
})

describe('filterContactRows', () => {
  const rows = [
    { key: '1', label: '马文涛 <mawentao@ouc-online.com.cn>', email: 'mawentao@ouc-online.com.cn', name: '马文涛' },
    { key: '2', label: '刘恺健 <liukaijian@ouc-online.com.cn>', email: 'liukaijian@ouc-online.com.cn', name: '刘恺健' },
  ]

  it('filters by name or email substring', () => {
    expect(filterContactRows(rows, '马文').map((r) => r.key)).toEqual(['1'])
    expect(filterContactRows(rows, 'liukaijian').map((r) => r.key)).toEqual(['2'])
  })

  it('returns all when query empty', () => {
    expect(filterContactRows(rows, '  ')).toHaveLength(2)
  })
})
