// 邮箱业务页数据层：列表加载、草稿 CAS 保存、通信录、发件箱投递状态。
// 账号是否可用只读 GetMailSettings；真实 SMTP 由后端 dispatcher 异步完成。
import { useCallback, useEffect, useState } from 'react'
import {
  confirmMailSend, createMailDraft, deleteMailContact, deleteMailDraft, errText,
  getMailDraft, getMailSettings, listMailContacts, listMailDrafts, listMailOutbox,
  prepareMailSend, saveMailContact, saveMailDraft,
  type ConfirmMailSendResult, type MailContactView, type MailDraftView,
  type MailOutboxEntryView, type MailSettings, type PrepareMailSendResult,
  type SaveMailDraftRequest,
} from '@/core/bridge'

export type MailTab = 'drafts' | 'contacts' | 'outbox'

export function useMail() {
  const [settings, setSettings] = useState<MailSettings | null>(null)
  const [drafts, setDrafts] = useState<MailDraftView[]>([])
  const [contacts, setContacts] = useState<MailContactView[]>([])
  const [outbox, setOutbox] = useState<MailOutboxEntryView[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const reload = useCallback(async () => {
    setError('')
    try {
      const [s, d, c, o] = await Promise.all([
        getMailSettings(),
        listMailDrafts({ limit: 50 }),
        listMailContacts({ limit: 50 }),
        listMailOutbox({ limit: 50 }),
      ])
      setSettings(s)
      setDrafts(d ?? [])
      setContacts(c ?? [])
      setOutbox(o ?? [])
    } catch (e) {
      setError(errText(e) || '加载失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void reload() }, [reload])

  const hasAccount = !!settings?.account?.enabled && !!settings.account.hasPassword

  async function loadDraft(id: string): Promise<MailDraftView> {
    return getMailDraft({ id })
  }

  async function createDraft(): Promise<MailDraftView> {
    const d = await createMailDraft({ accountId: settings?.account?.id })
    await reload()
    return d
  }

  async function persistDraft(req: SaveMailDraftRequest): Promise<MailDraftView> {
    const saved = await saveMailDraft(req)
    await reload()
    return saved
  }

  async function removeDraft(id: string): Promise<void> {
    await deleteMailDraft({ id })
    await reload()
  }

  async function addContact(name: string, email: string): Promise<void> {
    await saveMailContact({ name, emails: [email], expectedRevision: 0 })
    await reload()
  }

  async function removeContact(id: string, expectedRevision: number): Promise<void> {
    await deleteMailContact({ id, expectedRevision })
    await reload()
  }

  async function prepareSend(draftId: string, expectedRevision: number): Promise<PrepareMailSendResult> {
    return prepareMailSend({ draftId, expectedRevision })
  }

  async function confirmSend(confirmationId: string): Promise<ConfirmMailSendResult> {
    const res = await confirmMailSend({ confirmationId })
    await reload()
    return res
  }

  return {
    settings, drafts, contacts, outbox, loading, error, setError,
    hasAccount, reload, loadDraft, createDraft, persistDraft, removeDraft,
    addContact, removeContact,
    prepareSend, confirmSend,
  }
}
