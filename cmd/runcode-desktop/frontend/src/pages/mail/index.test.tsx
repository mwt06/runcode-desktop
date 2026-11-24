// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  getMailSettings: vi.fn(),
  listMailDrafts: vi.fn(),
  listMailContacts: vi.fn(),
  listMailOutbox: vi.fn(),
  getMailDraft: vi.fn(),
  createMailDraft: vi.fn(),
  saveMailDraft: vi.fn(),
  deleteMailDraft: vi.fn(),
  saveMailContact: vi.fn(),
  deleteMailContact: vi.fn(),
}))

vi.mock('@/core/bridge', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/core/bridge')>()
  return {
    ...actual,
    ...api,
    errText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  }
})

import { MailPage } from './index'

let host: HTMLDivElement
let root: Root
const onOpenSettings = vi.fn()

const draftA = {
  id: 'd1',
  revision: 1,
  status: 'draft',
  accountId: '',
  subject: '主题甲',
  bodyMarkdown: '正文 **甲**',
  to: [{ address: 'a@example.com' }],
  cc: [],
  bcc: [],
  sendMode: 'together',
  dispatchMode: 'immediate',
  updatedAtUtc: '2026-01-01T00:00:00Z',
  createdAtUtc: '2026-01-01T00:00:00Z',
}

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  localStorage.clear()
  for (const fn of Object.values(api)) fn.mockReset()
  onOpenSettings.mockReset()
  api.getMailSettings.mockResolvedValue({ secretStorageOk: true })
  api.listMailDrafts.mockResolvedValue([draftA])
  api.listMailContacts.mockResolvedValue([])
  api.listMailOutbox.mockResolvedValue([])
  api.getMailDraft.mockResolvedValue(draftA)
  api.deleteMailDraft.mockResolvedValue(undefined)
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
    await Promise.resolve()
  })
}

async function mount() {
  await act(async () => { root.render(<MailPage onOpenSettings={onOpenSettings} />) })
  await flush()
}

async function click(label: string) {
  const btn = Array.from(host.querySelectorAll('button')).find((b) => b.textContent?.includes(label))
  if (!btn) throw new Error(`button ${label} missing in ${host.textContent}`)
  await act(async () => { (btn as HTMLButtonElement).click() })
  await flush()
}

describe('MailPage', () => {
  it('shows no-account guidance without a password form', async () => {
    await mount()
    expect(host.querySelector('[data-testid="mail-no-account"]')).toBeTruthy()
    expect(host.textContent).toContain('去设置接入邮箱')
    expect(host.querySelector('input[type="password"]')).toBeNull()
    await click('前往邮箱设置')
    expect(onOpenSettings).toHaveBeenCalled()
  })

  it('renders tabs without groups or CSV import', async () => {
    await mount()
    expect(host.textContent).toContain('草稿（1）')
    expect(host.textContent).toContain('通信录')
    expect(host.textContent).toContain('发件箱（0）')
    expect(host.textContent).not.toContain('分组')
    await click('通信录')
    expect(host.textContent).not.toContain('CSV 导入')
    expect(host.textContent).not.toContain('预览并导入')
  })

  it('saves cc with draft and opens contact picker', async () => {
    api.listMailContacts.mockResolvedValue([{
      id: 'c1', name: '马文涛', emails: ['mawentao@ouc-online.com.cn'], revision: 1,
      createdAtUtc: '2026-01-01T00:00:00Z', updatedAtUtc: '2026-01-01T00:00:00Z',
    }])
    api.saveMailDraft.mockResolvedValue({
      ...draftA, revision: 2, cc: [{ address: 'cc@example.com' }],
    })
    await mount()
    await click('主题甲')
    const cc = host.querySelector('[data-testid="mail-draft-cc"]') as HTMLInputElement
    expect(cc).toBeTruthy()
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
      setter.call(cc, 'cc@example.com')
      cc.dispatchEvent(new InputEvent('input', { bubbles: true }))
    })
    await click('保存草稿')
    expect(api.saveMailDraft).toHaveBeenCalledWith(expect.objectContaining({
      cc: [{ address: 'cc@example.com' }],
    }))
    const pickCc = host.querySelector('[data-testid="mail-pick-cc"]') as HTMLButtonElement
    await act(async () => { pickCc.click() })
    await flush()
    expect(host.querySelector('[data-testid="mail-contact-picker"]')).toBeTruthy()
    const box = host.querySelector('[data-testid="mail-contact-picker"] input[type="checkbox"]') as HTMLInputElement
    await act(async () => { box.click() })
    await click('加入')
    expect((host.querySelector('[data-testid="mail-draft-cc"]') as HTMLInputElement).value)
      .toContain('mawentao@ouc-online.com.cn')
  })

  it('hides meta line and markdown toggle; preview sits after save', async () => {
    await mount()
    await click('主题甲')
    expect(host.querySelector('[data-testid="mail-draft-status"]')).toBeNull()
    expect(host.textContent).not.toContain('Markdown编辑')
    expect(host.textContent).not.toContain('含模型提案原文')
    const save = host.querySelector('[data-testid="mail-save-draft"]')
    const preview = host.querySelector('[data-testid="mail-preview-toggle"]')
    expect(save && preview && (save.compareDocumentPosition(preview) & Node.DOCUMENT_POSITION_FOLLOWING)).toBeTruthy()
    expect(host.textContent).toContain('发送')
    expect(host.textContent).not.toContain('检查并发送')
  })

  it('opens shared preview modal', async () => {
    await mount()
    await click('主题甲')
    await click('预览')
    expect(host.querySelector('[data-testid="mail-preview-modal"]')).toBeTruthy()
    expect(host.textContent).toContain('主题甲')
  })

  it('shows delete draft control and confirms before delete', async () => {
    await mount()
    await click('主题甲')
    const del = host.querySelector('[data-testid="mail-delete-draft"]') as HTMLButtonElement
    expect(del).toBeTruthy()
    await act(async () => { del.click() })
    await flush()
    expect(host.textContent).toContain('删除草稿？')
    await click('确认删除')
    expect(api.deleteMailDraft).toHaveBeenCalledWith({ id: 'd1' })
  })

  it('loads draft and keeps local edits when save fails', async () => {
    api.saveMailDraft.mockRejectedValue(new Error('冲突：草稿已被其它位置更新'))
    await mount()
    await click('主题甲')
    const subject = host.querySelector('[data-testid="mail-draft-subject"]') as HTMLInputElement
    const body = host.querySelector('[data-testid="mail-draft-body"]') as HTMLTextAreaElement
    expect(subject.value).toBe('主题甲')
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
      setter.call(subject, '本地新主题')
      subject.dispatchEvent(new InputEvent('input', { bubbles: true }))
      const tsetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!
      tsetter.call(body, '本地新正文')
      body.dispatchEvent(new InputEvent('input', { bubbles: true }))
    })
    await click('保存草稿')
    expect(api.saveMailDraft).toHaveBeenCalled()
    expect((host.querySelector('[data-testid="mail-draft-subject"]') as HTMLInputElement).value).toBe('本地新主题')
    expect((host.querySelector('[data-testid="mail-draft-body"]') as HTMLTextAreaElement).value).toBe('本地新正文')
    expect(host.textContent).toMatch(/保存失败|冲突/)
  })

  it('clears stale send notice when saving draft and shows Chinese validation errors', async () => {
    api.saveMailDraft
      .mockRejectedValueOnce(new Error('mail: invalid input: empty body'))
      .mockResolvedValueOnce({
        id: 'd1', revision: 2, subject: '主题甲', bodyMarkdown: '本地新正文',
        to: [{ address: 'a@example.com' }], status: 'draft', accountId: 'acc1',
      })
    await mount()
    await click('主题甲')
    // 模拟先前发送遗留的成功提示。
    const subject = host.querySelector('[data-testid="mail-draft-subject"]') as HTMLInputElement
    const body = host.querySelector('[data-testid="mail-draft-body"]') as HTMLTextAreaElement
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
      setter.call(subject, '主题甲')
      subject.dispatchEvent(new InputEvent('input', { bubbles: true }))
      const tsetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!
      tsetter.call(body, '')
      body.dispatchEvent(new InputEvent('input', { bubbles: true }))
    })
    await click('保存草稿')
    expect(host.textContent).toContain('请填写正文内容')
    expect(host.textContent).not.toContain('邮件已发送')
    await act(async () => {
      const tsetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!
      tsetter.call(body, '本地新正文')
      body.dispatchEvent(new InputEvent('input', { bubbles: true }))
    })
    await click('保存草稿')
    expect(host.querySelector('[data-testid="mail-send-notice"]')?.textContent).toContain('草稿已保存')
    expect(host.textContent).not.toContain('邮件已发送')
  })

  it('outbox tab lists delivery rows and expands body read-only', async () => {
    api.listMailOutbox.mockResolvedValue([{
      itemId: 'item1',
      submissionId: 'sub1',
      draftId: 'd1',
      draftRevision: 1,
      subject: '请假申请',
      bodyMarkdown: '正文内容',
      recipient: 'a@example.com',
      status: 'accepted',
      createdAtUtc: '2026-01-01T00:00:00Z',
      attemptCount: 1,
    }])
    await mount()
    await click('发件箱（1）')
    expect(host.querySelector('[data-testid="mail-outbox"]')).toBeTruthy()
    expect(host.textContent).toContain('请假申请')
    expect(host.textContent).toContain('已送达')
    await click('请假申请')
    expect(host.querySelector('[data-testid="mail-outbox-body"]')?.textContent).toContain('正文内容')
    expect(api.saveMailDraft).not.toHaveBeenCalled()
    expect(api.createMailDraft).not.toHaveBeenCalled()
  })
})
