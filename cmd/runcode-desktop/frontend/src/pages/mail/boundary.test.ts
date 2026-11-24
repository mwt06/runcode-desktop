import { describe, expect, it } from 'vitest'

/**
 * 邮件业务面不得 import 自动化页面 / hook / fixture。
 * 更多菜单是公共导航接缝（含 automation 字样），故意不扫 more-menu。
 */
const mailSources = {
  ...import.meta.glob('./**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true }),
  ...import.meta.glob('../settings/mail*.tsx', { query: '?raw', import: 'default', eager: true }),
  ...import.meta.glob('../../session/use-mail.ts', { query: '?raw', import: 'default', eager: true }),
  ...import.meta.glob('../../core/mail-settings*.ts', { query: '?raw', import: 'default', eager: true }),
  ...import.meta.glob('../../chat/mail-draft-actions*.ts*', { query: '?raw', import: 'default', eager: true }),
} as Record<string, string>

const FORBIDDEN = [
  /from\s+['"][^'"]*pages\/automation[^'"]*['"]/,
  /from\s+['"][^'"]*use-automation[^'"]*['"]/,
  /from\s+['"][^'"]*automation\/[^'"]*['"]/,
  /from\s+['"]@\/pages\/automation['"]/,
  /from\s+['"]@\/session\/use-automation['"]/,
]

describe('mail frontend automation boundary', () => {
  it('does not import automation pages/hooks/fixtures', () => {
    const entries = Object.entries(mailSources).filter(([path]) => !path.endsWith('boundary.test.ts'))
    expect(entries.length).toBeGreaterThan(0)
    const offenders: string[] = []
    for (const [path, text] of entries) {
      for (const re of FORBIDDEN) {
        if (re.test(text)) offenders.push(`${path} matches ${re}`)
      }
    }
    expect(offenders).toEqual([])
  })
})
