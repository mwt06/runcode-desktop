// 邮箱设置纯函数：版本→主机、表单指纹、向导步骤推进。不碰 automation。
import { MailVersions, type MailVersion } from './bridge'

export type MailWizardStep = 1 | 2 | 3 | 4 | 5

export type MailFormDraft = {
  address: string
  displayName: string
  version: MailVersion
  password: string
}

export function mailHostForVersion(version: MailVersion | string): string {
  switch (version) {
    case MailVersions.XC: return 'smtp.xc.aliyun.com'
    case MailVersions.HK: return 'smtphk.qiye.aliyun.com'
    default: return 'smtp.qiye.aliyun.com'
  }
}

export function mailWebmailPage(version: MailVersion | string): 'webmail_standard' | 'webmail_xc' {
  return version === MailVersions.XC ? 'webmail_xc' : 'webmail_standard'
}

/** 地址/版本/密码任一变化即作废旧连接测试。 */
export function mailFormFingerprint(draft: Pick<MailFormDraft, 'address' | 'version' | 'password'>): string {
  return `${draft.address.trim().toLowerCase()}\n${draft.version}\n${draft.password}`
}

export function mailTestStillValid(
  testedFingerprint: string | null,
  draft: Pick<MailFormDraft, 'address' | 'version' | 'password'>,
): boolean {
  return !!testedFingerprint && testedFingerprint === mailFormFingerprint(draft)
}

export function canAdvanceFromStep1(address: string): boolean {
  const a = address.trim()
  return a.includes('@') && a.includes('.')
}

export function initialWizardStep(hasAccount: boolean, status: string | undefined): MailWizardStep {
  if (!hasAccount) return 1
  switch (status) {
    case 'test_mail_accepted': return 5
    case 'receipt_confirmed': return 5
    case 'saved': return 5
    default: return 1
  }
}
