// 从本地通信录多选收件人 / 抄送，合并进现有逗号分隔字段（去重、保留已手填地址）。
import { useMemo, useState } from 'react'
import type { MailContactView } from '@/core/bridge'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { FIELD_CLS } from '@/ui/fields'

export type ContactPickerTarget = 'to' | 'cc'

export function mergeContactEmails(existing: string, emails: string[]): string {
  const seen = new Set<string>()
  const out: string[] = []
  for (const part of existing.split(/[,;，；\s]+/)) {
    const a = part.trim().toLowerCase()
    if (!a || seen.has(a)) continue
    seen.add(a)
    out.push(part.trim())
  }
  for (const raw of emails) {
    const a = raw.trim().toLowerCase()
    if (!a || seen.has(a)) continue
    seen.add(a)
    out.push(raw.trim())
  }
  return out.join(', ')
}

export function filterContactRows(
  rows: { key: string; label: string; email: string; name: string }[],
  query: string,
): typeof rows {
  const q = query.trim().toLowerCase()
  if (!q) return rows
  return rows.filter((r) =>
    r.label.toLowerCase().includes(q)
    || r.email.toLowerCase().includes(q)
    || r.name.toLowerCase().includes(q),
  )
}

export function ContactPickerModal({
  contacts,
  target,
  onConfirm,
  onClose,
}: {
  contacts: MailContactView[]
  target: ContactPickerTarget
  onConfirm: (emails: string[]) => void
  onClose: () => void
}) {
  const [selected, setSelected] = useState<Record<string, boolean>>({})
  const [query, setQuery] = useState('')
  const rows = useMemo(() => {
    const list: { key: string; label: string; email: string; name: string }[] = []
    for (const c of contacts) {
      for (const email of c.emails ?? []) {
        const e = email.trim()
        if (!e) continue
        const name = c.name?.trim() || ''
        list.push({
          key: `${c.id}:${e.toLowerCase()}`,
          label: name ? `${name} <${e}>` : e,
          email: e,
          name,
        })
      }
    }
    return list
  }, [contacts])

  const visible = useMemo(() => filterContactRows(rows, query), [rows, query])
  const picked = rows.filter((r) => selected[r.key]).map((r) => r.email)
  const title = target === 'cc' ? '选择抄送' : '选择收件人'

  return (
    <div
      className="fixed inset-0 bg-[rgba(30,33,50,0.32)] backdrop-blur-[2px] flex items-center justify-center z-30"
      data-testid="mail-contact-picker"
      onClick={onClose}
    >
      <div
        className="w-[440px] max-w-[94vw] max-h-[80vh] overflow-hidden bg-surface rounded-2xl shadow-modal flex flex-col"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between gap-3 px-5 pt-5 pb-2">
          <h3 className="m-0 text-[16px] font-bold text-ink">{title}</h3>
          <button type="button" className={BTN} onClick={onClose} aria-label="关闭">关闭</button>
        </div>
        <div className="px-5 pb-2">
          <input
            className={FIELD_CLS}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索姓名或邮箱"
            data-testid="mail-contact-picker-search"
            autoFocus
          />
        </div>
        <div className="flex-1 min-h-0 overflow-y-auto px-5 pb-3">
          {rows.length === 0 ? (
            <p className="text-[13px] text-muted m-0">
              通信录为空。可先在「通信录」页添加，或在对话中让助手写入联系人。
            </p>
          ) : visible.length === 0 ? (
            <p className="text-[13px] text-muted m-0" data-testid="mail-contact-picker-empty">
              没有匹配「{query.trim()}」的联系人
            </p>
          ) : (
            <ul className="m-0 p-0 list-none flex flex-col gap-1">
              {visible.map((r) => (
                <li key={r.key}>
                  <label className="flex items-start gap-2 text-[13px] text-ink cursor-pointer py-1">
                    <input
                      type="checkbox"
                      className="mt-0.5"
                      checked={!!selected[r.key]}
                      onChange={(e) => setSelected((prev) => ({ ...prev, [r.key]: e.target.checked }))}
                    />
                    <span className="break-words [overflow-wrap:anywhere]">{r.label}</span>
                  </label>
                </li>
              ))}
            </ul>
          )}
        </div>
        <div className="flex justify-end gap-2.5 px-5 py-4 border-t border-line2">
          <button type="button" className={BTN} onClick={onClose}>取消</button>
          <button
            type="button"
            className={`${BTN} ${BTN_PRIMARY}`}
            disabled={picked.length === 0}
            data-testid="mail-contact-picker-confirm"
            onClick={() => onConfirm(picked)}
          >
            加入（{picked.length}）
          </button>
        </div>
      </div>
    </div>
  )
}
