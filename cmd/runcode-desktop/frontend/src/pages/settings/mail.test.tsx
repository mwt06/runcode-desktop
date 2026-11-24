// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  getMailSettings: vi.fn(),
  saveMailAccount: vi.fn(),
  testMailConnection: vi.fn(),
  sendMailTestToSelf: vi.fn(),
  confirmMailTestReceipt: vi.fn(),
  openMailOfficialURL: vi.fn(),
}))

vi.mock('@/core/bridge', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/core/bridge')>()
  return {
    ...actual,
    ...api,
    errText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  }
})

import { MailSection } from './mail'

let host: HTMLDivElement
let root: Root

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  for (const fn of Object.values(api)) fn.mockReset()
  api.getMailSettings.mockResolvedValue({ secretStorageOk: true })
  api.openMailOfficialURL.mockResolvedValue(undefined)
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

async function flush() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

async function mount() {
  await act(async () => { root.render(<MailSection />) })
  await flush()
}

async function click(label: string) {
  const btn = Array.from(host.querySelectorAll('button')).find((b) => b.textContent?.includes(label))
  if (!btn) throw new Error(`button ${label} missing in ${host.textContent}`)
  await act(async () => { (btn as HTMLButtonElement).click() })
}

function inputByLabel(label: string): HTMLInputElement {
  const lab = Array.from(host.querySelectorAll('label')).find((el) => el.textContent?.includes(label))
  const input = lab?.querySelector('input:not([type="checkbox"])') as HTMLInputElement | null
  if (!input) throw new Error(`input ${label}`)
  return input
}

async function typeLabeled(label: string, value: string) {
  const input = inputByLabel(label)
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    setter.call(input, value)
    input.dispatchEvent(new InputEvent('input', { bubbles: true, data: value, inputType: 'insertText' }))
  })
}

describe('MailSection wizard', () => {
  it('lists mail start entry under settings section', async () => {
    await mount()
    expect(host.textContent).toContain('开始接入')
    expect(host.textContent).toContain('邮箱')
  })

  it('advances step 1 with a full address without a session-disabled gate', async () => {
    await mount()
    await click('开始接入')
    await typeLabeled('邮箱地址', 'name@corp.com')
    const next = Array.from(host.querySelectorAll('button')).find((b) => b.textContent?.includes('下一步')) as HTMLButtonElement
    expect(next.disabled).toBe(false)
  })

  it('invalidates stale connection test when password changes', async () => {
    api.testMailConnection.mockResolvedValue({
      ok: true,
      message: '连接测试通过。已用当前填写的账号登录发信服务器；尚未保存，也未发送邮件。',
      configFingerprint: 'fp-1',
    })
    await mount()
    await click('开始接入')
    await typeLabeled('邮箱地址', 'name@corp.com')
    await click('下一步')
    await click('填写连接信息')
    await typeLabeled('专用安全密码', 'old-pass')
    await click('测试连接')
    await flush()
    expect(host.textContent).toContain('连接测试通过')
    await typeLabeled('专用安全密码', 'new-pass')
    const saveBtn = Array.from(host.querySelectorAll('button')).find((b) => b.textContent?.includes('保存邮箱账号')) as HTMLButtonElement
    expect(saveBtn.disabled).toBe(true)
  })

  it('keeps save incomplete when backend save fails', async () => {
    api.testMailConnection.mockResolvedValue({
      ok: true,
      message: '连接测试通过。已用当前填写的账号登录发信服务器；尚未保存，也未发送邮件。',
      configFingerprint: 'fp-1',
    })
    api.saveMailAccount.mockRejectedValue(new Error('当前系统无法安全保存邮箱密码'))
    await mount()
    await click('开始接入')
    await typeLabeled('邮箱地址', 'name@corp.com')
    await click('下一步')
    await click('填写连接信息')
    await typeLabeled('专用安全密码', 'pass')
    await click('测试连接')
    await flush()
    await click('保存邮箱账号')
    await flush()
    await click('确认安全保存')
    await flush()
    expect(host.textContent).toContain('当前系统无法安全保存邮箱密码')
    expect(host.textContent).not.toContain('接入与自测完成')
  })

  it('shows unknown without auto retry after self-test', async () => {
    api.getMailSettings.mockResolvedValue({
      secretStorageOk: true,
      account: {
        id: 'm1', address: 'me@corp.com', displayName: '', version: 'standard',
        host: 'smtp.qiye.aliyun.com', port: 465, enabled: true, hasPassword: true,
        revision: 1, credentialRevision: 1, onboardingStatus: 'saved',
      },
    })
    api.sendMailTestToSelf.mockResolvedValue({
      status: 'unknown', shortId: 'abc123', submissionId: 'sub1',
      message: '发送结果待核实：邮件可能已发出。请先按短编号搜索，不要自动重发。',
    })
    await mount()
    expect(host.textContent).toContain('发送测试邮件')
    const receiptBefore = host.querySelector('[data-testid="mail-confirm-receipt"]') as HTMLButtonElement
    expect(receiptBefore.disabled).toBe(true)
    await click('发送测试邮件')
    await flush()
    expect(host.textContent).toContain('不要自动重发')
    expect(api.sendMailTestToSelf).toHaveBeenCalledTimes(1)
    const receiptAfter = host.querySelector('[data-testid="mail-confirm-receipt"]') as HTMLButtonElement
    expect(receiptAfter.disabled).toBe(false)
  })

  it('switches to configured summary after receipt confirm', async () => {
    api.getMailSettings.mockResolvedValue({
      secretStorageOk: true,
      account: {
        id: 'm1', address: 'me@corp.com', displayName: '', version: 'standard',
        host: 'smtp.qiye.aliyun.com', port: 465, enabled: true, hasPassword: true,
        revision: 1, credentialRevision: 1, onboardingStatus: 'test_mail_accepted',
        lastTestShortId: 'abc123',
      },
    })
    api.sendMailTestToSelf.mockResolvedValue({
      status: 'accepted', shortId: 'abc123', submissionId: 'sub1',
      message: '已发送',
    })
    api.confirmMailTestReceipt.mockResolvedValue({
      secretStorageOk: true,
      account: {
        id: 'm1', address: 'me@corp.com', displayName: '', version: 'standard',
        host: 'smtp.qiye.aliyun.com', port: 465, enabled: true, hasPassword: true,
        revision: 2, credentialRevision: 1, onboardingStatus: 'receipt_confirmed',
        lastTestShortId: 'abc123',
      },
    })
    await mount()
    expect(host.textContent).toContain('已收到邮件')
    await click('已收到邮件')
    await flush()
    expect(host.textContent).toContain('重新配置 / 更新安全密码')
    expect(host.textContent).toContain('接入与自测完成')
    expect(api.confirmMailTestReceipt).toHaveBeenCalledTimes(1)
  })

  it('keeps official help on step 2 and does not collect codes', async () => {
    await mount()
    await click('开始接入')
    await typeLabeled('邮箱地址', 'name@corp.com')
    await click('下一步')
    expect(host.textContent).toContain('打开官方图文帮助')
    expect(host.textContent).toContain('不会代收手机验证码')
    expect(host.textContent).not.toContain('遇到问题')
    expect(host.textContent).not.toContain('收起问题帮助')
    expect(host.querySelector('input[name="sms"]')).toBeNull()
  })

  it('clears password draft on unmount', async () => {
    await mount()
    await click('开始接入')
    await typeLabeled('邮箱地址', 'name@corp.com')
    await click('下一步')
    await click('填写连接信息')
    await typeLabeled('专用安全密码', 'should-not-persist')
    expect(inputByLabel('专用安全密码').value).toBe('should-not-persist')
    await act(async () => root.unmount())
    // Remount: password must not come from localStorage or prior draft persistence.
    root = createRoot(host)
    await mount()
    await click('开始接入')
    await typeLabeled('邮箱地址', 'name@corp.com')
    await click('下一步')
    await click('填写连接信息')
    expect(inputByLabel('专用安全密码').value).toBe('')
  })
})
