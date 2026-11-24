// 更多 → 邮箱与通信录：草稿 / 通信录 / 发件箱。
// 账号配置只跳转设置，本页不收集安全密码；编辑保存不发信。
import { useEffect, useMemo, useState } from 'react'
import { errText, type MailDraftView, type PrepareMailSendResult } from '@/core/bridge'
import { formatMailUserError } from '@/core/mail-user-error'
import { useMail, type MailTab } from '@/session/use-mail'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { InlineError } from '@/ui/feedback'
import { FIELD_CLS, LABEL_CLS } from '@/ui/fields'
import { PageShell, Placeholder } from '@/ui/layout'
import { ConfirmDialog } from '@/ui/confirm-dialog'
import { isComposingKey } from '@/ui/keys'
import { ContactPickerModal, mergeContactEmails, type ContactPickerTarget } from './contact-picker-modal'
import { clearDraftSeen, isDraftSeen, markDraftSeen } from './draft-seen'
import { MailMessageModal } from './mail-message-modal'
import { MailSafePreview } from './safe-preview'

function joinAddresses(addrs: { name?: string; address: string }[] | null | undefined): string {
  return (addrs ?? []).map((a) => a.address).filter(Boolean).join(', ')
}

function formatRecipients(addrs: { name?: string; address: string }[] | null | undefined): string {
  return (addrs ?? []).map((a) => {
    const addr = a.address?.trim()
    if (!addr) return ''
    const name = a.name?.trim()
    return name ? `${name} <${addr}>` : addr
  }).filter(Boolean).join(', ')
}

function parseAddresses(raw: string): { address: string }[] {
  return raw.split(/[,;，；\s]+/).map((s) => s.trim()).filter(Boolean).map((address) => ({ address }))
}

function statusLabel(status: string | undefined): string {
  switch (status) {
    case 'draft': return '草稿'
    case 'queued': return '已入队'
    case 'scheduled': return '已排期'
    case 'sending': return '发送中'
    case 'accepted': return '已送达'
    case 'failed': return '失败'
    case 'unknown': return '结果未知'
    case 'missed': return '错过窗口'
    case 'cancelled': return '已取消'
    default: return status || '—'
  }
}

export function MailPage({
  onOpenSettings,
  initialDraftId,
}: {
  onOpenSettings: () => void
  initialDraftId?: string
}) {
  const mail = useMail()
  const [tab, setTab] = useState<MailTab>('drafts')
  const [activeDraftId, setActiveDraftId] = useState<string | null>(initialDraftId ?? null)
  const [draft, setDraft] = useState<MailDraftView | null>(null)
  const [editSubject, setEditSubject] = useState('')
  const [editBody, setEditBody] = useState('')
  const [editTo, setEditTo] = useState('')
  const [editCc, setEditCc] = useState('')
  const [saving, setSaving] = useState(false)
  const [leaveOpen, setLeaveOpen] = useState(false)
  const [pendingTab, setPendingTab] = useState<MailTab | null>(null)
  const [pendingDraftId, setPendingDraftId] = useState<string | null>(null)
  const [contactName, setContactName] = useState('')
  const [contactEmail, setContactEmail] = useState('')
  const [pickerTarget, setPickerTarget] = useState<ContactPickerTarget | null>(null)
  const [sendPrep, setSendPrep] = useState<PrepareMailSendResult | null>(null)
  const [previewOpen, setPreviewOpen] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [notice, setNotice] = useState('')
  const [deleteTargetId, setDeleteTargetId] = useState<string | null>(null)
  const [seenTick, setSeenTick] = useState(0)
  const [expandedOutboxId, setExpandedOutboxId] = useState<string | null>(null)

  const dirty = !!draft && (
    editSubject !== draft.subject
    || editBody !== draft.bodyMarkdown
    || editTo !== joinAddresses(draft.to)
    || editCc !== joinAddresses(draft.cc)
  )

  useEffect(() => {
    if (initialDraftId) setActiveDraftId(initialDraftId)
  }, [initialDraftId])

  useEffect(() => {
    if (!activeDraftId) {
      setDraft(null)
      return
    }
    let cancelled = false
    void mail.loadDraft(activeDraftId).then((d) => {
      if (cancelled) return
      setDraft(d)
      setEditSubject(d.subject)
      setEditBody(d.bodyMarkdown)
      setEditTo(joinAddresses(d.to))
      setEditCc(joinAddresses(d.cc))
      markDraftSeen(d.id)
      setSeenTick((n) => n + 1)
    }).catch((e) => {
      if (!cancelled) mail.setError(errText(e))
    })
    return () => { cancelled = true }
    // 仅随草稿 id 换载；mail.loadDraft 稳定于 store。
    // eslint-disable-next-line react-hooks/exhaustive-deps -- intentional id-only reload
  }, [activeDraftId])

  function discardLocalEdits() {
    if (!draft) return
    setEditSubject(draft.subject)
    setEditBody(draft.bodyMarkdown)
    setEditTo(joinAddresses(draft.to))
    setEditCc(joinAddresses(draft.cc))
  }

  function applyLeave() {
    discardLocalEdits()
    if (pendingTab) setTab(pendingTab)
    if (pendingDraftId) setActiveDraftId(pendingDraftId)
    setPendingTab(null)
    setPendingDraftId(null)
    setLeaveOpen(false)
  }

  function requestTab(next: MailTab) {
    if (dirty && tab === 'drafts') {
      setPendingTab(next)
      setPendingDraftId(null)
      setLeaveOpen(true)
      return
    }
    // 切回草稿等操作时清掉发送成功提示，避免误当成「保存也算已发送」。
    if (next === 'drafts') setNotice('')
    setTab(next)
  }

  function requestDraft(id: string) {
    if (dirty && activeDraftId && activeDraftId !== id) {
      setPendingDraftId(id)
      setPendingTab(null)
      setLeaveOpen(true)
      return
    }
    setNotice('')
    setActiveDraftId(id)
  }

  async function saveDraft(current = draft, opts?: { announce?: boolean }): Promise<MailDraftView | null> {
    if (!current || saving) return null
    setSaving(true)
    mail.setError('')
    // 保存草稿绝不沿用「邮件已发送」提示。
    setNotice('')
    try {
      const saved = await mail.persistDraft({
        id: current.id,
        expectedRevision: current.revision,
        accountId: current.accountId || mail.settings?.account?.id || '',
        subject: editSubject,
        bodyMarkdown: editBody,
        to: parseAddresses(editTo),
        cc: parseAddresses(editCc),
        bcc: current.bcc,
        sendMode: current.sendMode || 'together',
        dispatchMode: current.dispatchMode || 'immediate',
        scheduledLocal: current.scheduledLocal,
        timeZone: current.timeZone,
      })
      setDraft(saved)
      setEditSubject(saved.subject)
      setEditBody(saved.bodyMarkdown)
      setEditTo(joinAddresses(saved.to))
      setEditCc(joinAddresses(saved.cc))
      if (opts?.announce !== false) setNotice('草稿已保存')
      return saved
    } catch (e) {
      setNotice('')
      mail.setError(formatMailUserError(errText(e) || '保存失败，编辑已保留'))
      return null
    } finally {
      setSaving(false)
    }
  }

  async function openConfirmSend() {
    if (!draft) return
    if (dirty) {
      // 发送前静默落盘，不提示「草稿已保存」，避免与发信提示混淆。
      const saved = await saveDraft(undefined, { announce: false })
      if (!saved) return
      await runPrepare(saved)
      return
    }
    await runPrepare(draft)
  }

  async function runPrepare(d: MailDraftView) {
    mail.setError('')
    setNotice('')
    try {
      const prep = await mail.prepareSend(d.id, d.revision)
      setSendPrep(prep)
      setPreviewOpen(false)
    } catch (e) {
      mail.setError(formatMailUserError(errText(e) || '检查失败'))
    }
  }

  async function confirmPrepared() {
    if (!sendPrep || confirming) return
    setConfirming(true)
    mail.setError('')
    setNotice('')
    try {
      const res = await mail.confirmSend(sendPrep.confirmationId)
      setSendPrep(null)
      if (res.status === 'accepted') {
        setNotice('邮件已发送（服务器已接受）。可在发件箱查看记录。')
      } else if (res.status === 'scheduled') {
        setNotice('已排期投递；发件箱可查看进度。')
      } else if (res.status === 'unknown') {
        setNotice('发送结果待核实：邮件可能已发出，请先查收件箱，不要立即重发。')
      } else if (res.status === 'failed') {
        mail.setError(res.detail ? `发送失败：${res.detail}` : '发送失败，请查看发件箱中的原因')
        setNotice('')
      } else {
        setNotice(`已提交（${res.status}）；发件箱可查看投递结果。`)
      }
      setDraft(null)
      setActiveDraftId(null)
      setTab('outbox')
      await mail.reload()
    } catch (e) {
      mail.setError(errText(e) || '确认失败，未发送')
    } finally {
      setConfirming(false)
    }
  }

  async function newDraft() {
    mail.setError('')
    setNotice('')
    try {
      const d = await mail.createDraft()
      setActiveDraftId(d.id)
      setTab('drafts')
    } catch (e) {
      mail.setError(formatMailUserError(errText(e)))
    }
  }

  async function applyDeleteDraft() {
    const id = deleteTargetId
    if (!id) return
    setDeleteTargetId(null)
    mail.setError('')
    try {
      await mail.removeDraft(id)
      clearDraftSeen(id)
      setSeenTick((n) => n + 1)
      if (activeDraftId === id) {
        setActiveDraftId(null)
        setDraft(null)
      }
    } catch (e) {
      mail.setError(errText(e) || '删除失败')
    }
  }

  async function addContact() {
    try {
      await mail.addContact(contactName, contactEmail)
      setContactName('')
      setContactEmail('')
    } catch (e) {
      mail.setError(errText(e))
    }
  }

  function applyPickedContacts(emails: string[]) {
    if (pickerTarget === 'to') setEditTo((prev) => mergeContactEmails(prev, emails))
    if (pickerTarget === 'cc') setEditCc((prev) => mergeContactEmails(prev, emails))
    setPickerTarget(null)
  }

  const tabs = useMemo(() => ([
    { id: 'drafts' as const, label: `草稿（${mail.drafts.length}）` },
    { id: 'contacts' as const, label: '通信录' },
    { id: 'outbox' as const, label: `发件箱（${mail.outbox.length}）` },
  ]), [mail.drafts.length, mail.outbox.length])

  // seenTick：标记已读后强制重渲列表紫点。
  void seenTick

  return (
    <PageShell
      title="邮箱与通信录"
      width={960}
      action={(
        <button type="button" className={BTN} onClick={onOpenSettings}>
          前往邮箱设置
        </button>
      )}
    >
      {mail.error && <InlineError>{mail.error}</InlineError>}
      {notice && !mail.error && (
        <div className="rounded-field border border-line2 bg-surface2 px-3 py-2 text-[13px] text-ink" data-testid="mail-send-notice">
          {notice}
        </div>
      )}
      {!mail.hasAccount && (
        <div className="rounded-field border border-amber/40 bg-amber/10 px-3 py-2 text-[13px] text-amberink" data-testid="mail-no-account">
          尚未接入邮箱账号。仍可起草与维护通信录；发送请先
          <button type="button" className="ml-1 underline" onClick={onOpenSettings}>去设置接入邮箱</button>
          。本页不收集安全密码。
        </div>
      )}

      <div className="flex gap-1 border-b border-line2 pb-1" role="tablist" aria-label="邮箱分区">
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            role="tab"
            aria-selected={tab === t.id}
            className={`px-3 py-2 text-[15px] font-bold rounded-field cursor-pointer transition shadow-none hover:shadow-md ${tab === t.id ? 'bg-surface2 text-ink' : 'text-muted hover:text-ink'}`}
            onClick={() => requestTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </div>

      {mail.loading ? <Placeholder>加载中…</Placeholder> : null}

      {!mail.loading && tab === 'drafts' && (
        <div className="grid grid-cols-1 md:grid-cols-[220px_1fr] gap-4 min-h-0">
          <div className="flex flex-col gap-2">
            <button type="button" className={`${BTN} ${BTN_PRIMARY}`} onClick={() => void newDraft()}>新建草稿</button>
            <ul className="m-0 p-0 list-none flex flex-col gap-1" data-testid="mail-draft-list">
              {mail.drafts.map((d) => {
                const unseen = !isDraftSeen(d.id)
                return (
                  <li key={d.id} className="flex items-center gap-1">
                    <button
                      type="button"
                      className={`flex-1 min-w-0 text-left px-2 py-2 rounded-field text-[13px] cursor-pointer ${activeDraftId === d.id ? 'bg-surface2 text-ink' : 'text-muted hover:bg-surface2'}`}
                      onClick={() => requestDraft(d.id)}
                    >
                      <span className="truncate block">{d.subject || '（无主题）'}</span>
                    </button>
                    {unseen ? (
                      <span className="w-2 h-2 rounded-full bg-primary flex-none" data-testid="mail-draft-unseen" title="待查看" />
                    ) : null}
                    <button
                      type="button"
                      className="flex-none inline-flex items-center justify-center w-5 h-5 text-[11px] leading-none text-muted border border-line2 rounded-field bg-surface hover:border-primary hover:text-primaryink cursor-pointer p-0"
                      title="删除"
                      aria-label="删除"
                      data-testid="mail-draft-list-delete"
                      onClick={(e) => {
                        e.stopPropagation()
                        setDeleteTargetId(d.id)
                      }}
                    >
                      ×
                    </button>
                  </li>
                )
              })}
            </ul>
          </div>
          <div className="flex flex-col gap-3 min-w-0">
            {!draft ? (
              <Placeholder>选择或新建草稿</Placeholder>
            ) : (
              <>
                <div className="flex items-start justify-between gap-3">
                  <label className={`${LABEL_CLS} flex-1 min-w-0`}>主题
                    <input
                      className={FIELD_CLS}
                      value={editSubject}
                      onChange={(e) => setEditSubject(e.target.value)}
                      data-testid="mail-draft-subject"
                    />
                  </label>
                  <button
                    type="button"
                    className={`${BTN} flex-none mt-5 text-red border-red bg-transparent`}
                    data-testid="mail-delete-draft"
                    onClick={() => setDeleteTargetId(draft.id)}
                  >
                    删除草稿
                  </button>
                </div>
                <div className="flex flex-col gap-1">
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-[13px] text-muted">收件人（逗号分隔）</span>
                    <button
                      type="button"
                      className={`${BTN} py-1 px-2 text-[12px]`}
                      data-testid="mail-pick-to"
                      onClick={() => setPickerTarget('to')}
                    >
                      从通信录选择
                    </button>
                  </div>
                  <input
                    className={FIELD_CLS}
                    value={editTo}
                    onChange={(e) => setEditTo(e.target.value)}
                    data-testid="mail-draft-to"
                  />
                </div>
                <div className="flex flex-col gap-1">
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-[13px] text-muted">抄送（逗号分隔）</span>
                    <button
                      type="button"
                      className={`${BTN} py-1 px-2 text-[12px]`}
                      data-testid="mail-pick-cc"
                      onClick={() => setPickerTarget('cc')}
                    >
                      从通信录选择
                    </button>
                  </div>
                  <input
                    className={FIELD_CLS}
                    value={editCc}
                    onChange={(e) => setEditCc(e.target.value)}
                    data-testid="mail-draft-cc"
                  />
                </div>
                <textarea
                  className={`${FIELD_CLS} min-h-[220px] resize-y font-mono text-[13px]`}
                  value={editBody}
                  onChange={(e) => setEditBody(e.target.value)}
                  data-testid="mail-draft-body"
                  onKeyDown={(e) => {
                    if (isComposingKey(e)) return
                  }}
                />
                <div className="flex flex-wrap gap-2">
                  <button
                    type="button"
                    className={`${BTN} ${BTN_PRIMARY}`}
                    disabled={!dirty || saving}
                    onClick={() => void saveDraft()}
                    data-testid="mail-save-draft"
                  >
                    {saving ? '保存中…' : '保存草稿'}
                  </button>
                  <button
                    type="button"
                    className={BTN}
                    data-testid="mail-preview-toggle"
                    onClick={() => {
                      setPreviewOpen(true)
                      setSendPrep(null)
                    }}
                  >
                    预览
                  </button>
                  <button
                    type="button"
                    className={BTN}
                    disabled={!dirty}
                    onClick={discardLocalEdits}
                  >
                    放弃未保存修改
                  </button>
                  <button
                    type="button"
                    className={`${BTN} ${BTN_PRIMARY}`}
                    disabled={saving || confirming || !mail.hasAccount}
                    onClick={() => void openConfirmSend()}
                    data-testid="mail-check-send"
                    title={!mail.hasAccount ? '请先接入邮箱账号' : undefined}
                  >
                    发送
                  </button>
                </div>
              </>
            )}
          </div>
        </div>
      )}

      {!mail.loading && tab === 'contacts' && (
        <div className="flex flex-col gap-4" data-testid="mail-contacts">
          <div className="flex flex-wrap gap-2 items-end">
            <label className={LABEL_CLS}>姓名
              <input className={FIELD_CLS} value={contactName} onChange={(e) => setContactName(e.target.value)} />
            </label>
            <label className={LABEL_CLS}>邮箱
              <input className={FIELD_CLS} value={contactEmail} onChange={(e) => setContactEmail(e.target.value)} />
            </label>
            <button type="button" className={`${BTN} ${BTN_PRIMARY}`} onClick={() => void addContact()}>添加</button>
          </div>
          <p className="text-[12px] text-muted m-0">也可在对话中说「把某某 email@x.com 加入通信录」，由助手写入（需您批准）。</p>
          <ul className="m-0 p-0 list-none flex flex-col gap-2">
            {mail.contacts.map((c) => (
              <li key={c.id} className="flex items-center justify-between border border-line2 rounded-field px-3 py-2 text-[13px]">
                <span>{c.name} · {(c.emails ?? []).join(', ')}</span>
                <button
                  type="button"
                  className={BTN}
                  onClick={() => void mail.removeContact(c.id, c.revision).catch((e) => mail.setError(errText(e)))}
                >
                  删除
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}

      {!mail.loading && tab === 'outbox' && (
        <div className="flex flex-col gap-3" data-testid="mail-outbox">
          <div className="flex justify-end">
            <button type="button" className={BTN} onClick={() => void mail.reload()}>刷新</button>
          </div>
          {mail.outbox.length === 0 ? (
            <Placeholder pad="lg">暂无投递记录。确认发送后会出现在这里。</Placeholder>
          ) : (
            <ul className="m-0 p-0 list-none flex flex-col gap-2">
              {mail.outbox.map((e) => {
                const open = expandedOutboxId === e.itemId
                return (
                  <li
                    key={e.itemId}
                    className="border border-line2 rounded-field px-3 py-2 text-[13px] flex flex-col gap-1"
                    data-testid="mail-outbox-row"
                  >
                    <button
                      type="button"
                      className="w-full text-left bg-transparent border-0 p-0 cursor-pointer"
                      onClick={() => setExpandedOutboxId(open ? null : e.itemId)}
                    >
                      <div className="flex flex-wrap items-baseline justify-between gap-2">
                        <span className="font-medium text-ink">{e.subject || '（无主题）'}</span>
                        <span className="text-[12px] text-muted">{statusLabel(e.status)}</span>
                      </div>
                      <div className="text-[12px] text-muted">收件人 {e.recipient || '—'}</div>
                    </button>
                    {e.blockedReason ? (
                      <div className="text-[12px] text-amberink">原因：{e.blockedReason}</div>
                    ) : null}
                    {open ? (
                      <div className="mt-2 border-t border-line2 pt-2 break-words [overflow-wrap:anywhere]" data-testid="mail-outbox-body">
                        <MailSafePreview markdown={e.bodyMarkdown || ''} />
                      </div>
                    ) : null}
                  </li>
                )
              })}
            </ul>
          )}
        </div>
      )}

      {leaveOpen && (
        <ConfirmDialog
          title="有未保存的修改"
          message="离开将丢失未保存的草稿编辑。"
          confirmLabel="放弃修改并离开"
          onCancel={() => {
            setLeaveOpen(false)
            setPendingTab(null)
            setPendingDraftId(null)
          }}
          onConfirm={applyLeave}
        />
      )}

      {deleteTargetId && (
        <ConfirmDialog
          title="删除草稿？"
          message="删除后不可恢复。"
          confirmLabel="确认删除"
          onCancel={() => setDeleteTargetId(null)}
          onConfirm={() => void applyDeleteDraft()}
        />
      )}

      {previewOpen && !sendPrep && (
        <MailMessageModal
          mode="preview"
          subject={editSubject}
          recipients={editTo}
          cc={editCc}
          bodyMarkdown={editBody}
          onClose={() => setPreviewOpen(false)}
        />
      )}

      {sendPrep && (
        <MailMessageModal
          mode="confirm-send"
          subject={sendPrep.preview.subject}
          recipients={formatRecipients(sendPrep.preview.to)}
          cc={formatRecipients(sendPrep.preview.cc)}
          bodyMarkdown={sendPrep.preview.bodyMarkdown}
          confirming={confirming}
          onClose={() => setSendPrep(null)}
          onConfirmSend={() => void confirmPrepared()}
        />
      )}

      {pickerTarget && (
        <ContactPickerModal
          contacts={mail.contacts}
          target={pickerTarget}
          onClose={() => setPickerTarget(null)}
          onConfirm={applyPickedContacts}
        />
      )}
    </PageShell>
  )
}
