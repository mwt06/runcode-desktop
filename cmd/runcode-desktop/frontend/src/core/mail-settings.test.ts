import { describe, expect, it } from 'vitest'
import { MailVersions } from './bridge'
import {
  canAdvanceFromStep1, initialWizardStep, mailFormFingerprint, mailHostForVersion,
  mailTestStillValid, mailWebmailPage,
} from './mail-settings'

describe('mail-settings helpers', () => {
  it('maps versions to fixed hosts', () => {
    expect(mailHostForVersion(MailVersions.Standard)).toBe('smtp.qiye.aliyun.com')
    expect(mailHostForVersion(MailVersions.XC)).toBe('smtp.xc.aliyun.com')
    expect(mailHostForVersion(MailVersions.HK)).toBe('smtphk.qiye.aliyun.com')
    expect(mailWebmailPage(MailVersions.XC)).toBe('webmail_xc')
  })

  it('invalidates connection test when password changes', () => {
    const draft = { address: 'a@example.com', version: MailVersions.Standard, password: 'one' }
    const fp = mailFormFingerprint(draft)
    expect(mailTestStillValid(fp, draft)).toBe(true)
    expect(mailTestStillValid(fp, { ...draft, password: 'two' })).toBe(false)
    expect(mailTestStillValid(fp, { ...draft, version: MailVersions.XC })).toBe(false)
  })

  it('requires a plausible address before step 1 advance', () => {
    expect(canAdvanceFromStep1('')).toBe(false)
    expect(canAdvanceFromStep1('nickname')).toBe(false)
    expect(canAdvanceFromStep1('name@corp.com')).toBe(true)
  })

  it('opens saved accounts on the self-test step', () => {
    expect(initialWizardStep(false, undefined)).toBe(1)
    expect(initialWizardStep(true, 'saved')).toBe(5)
    expect(initialWizardStep(true, 'receipt_confirmed')).toBe(5)
  })
})
