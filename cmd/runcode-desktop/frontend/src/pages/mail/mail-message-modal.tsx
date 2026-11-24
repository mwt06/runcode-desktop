// 预览与确认发送共用同一弹窗壳：主题 / 收件人 / 渲染正文；mode 只区分操作条。
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { MailSafePreview } from './safe-preview'

export type MailMessageModalMode = 'preview' | 'confirm-send'

export function MailMessageModal({
  mode,
  subject,
  recipients,
  cc,
  bodyMarkdown,
  confirming,
  onClose,
  onConfirmSend,
}: {
  mode: MailMessageModalMode
  subject: string
  recipients: string
  cc?: string
  bodyMarkdown: string
  confirming?: boolean
  onClose: () => void
  onConfirmSend?: () => void
}) {
  return (
    <div
      className="fixed inset-0 bg-[rgba(30,33,50,0.32)] backdrop-blur-[2px] flex items-center justify-center z-30"
      data-testid={mode === 'preview' ? 'mail-preview-modal' : 'mail-send-confirm'}
      onClick={onClose}
    >
      <div
        className="w-[720px] max-w-[94vw] max-h-[85vh] overflow-hidden bg-surface rounded-2xl shadow-modal flex flex-col"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-3 px-5 pt-5 pb-2">
          <h3 className="m-0 text-[18px] font-bold text-ink break-words min-w-0">
            {subject.trim() || '（无主题）'}
          </h3>
          <button type="button" className={`${BTN} flex-none`} onClick={onClose} aria-label="关闭">
            关闭
          </button>
        </div>
        <div className="px-5 pb-2 text-[13px] text-muted break-words">
          <div>
            <span className="text-faint">收件人</span>{' '}
            {recipients.trim() || '—'}
          </div>
          {cc?.trim() ? (
            <div className="mt-1" data-testid="mail-modal-cc">
              <span className="text-faint">抄送</span>{' '}
              {cc.trim()}
            </div>
          ) : null}
        </div>
        <div className="flex-1 min-h-0 overflow-y-auto overflow-x-hidden px-5 pb-4">
          <div className="border border-line2 rounded-field p-4 bg-surface2 break-words [overflow-wrap:anywhere]">
            <MailSafePreview markdown={bodyMarkdown} />
          </div>
        </div>
        {mode === 'confirm-send' ? (
          <div className="flex justify-end gap-2.5 px-5 py-4 border-t border-line2">
            <button type="button" className={BTN} onClick={onClose} disabled={confirming}>
              返回编辑
            </button>
            <button
              type="button"
              className={`${BTN} ${BTN_PRIMARY}`}
              disabled={confirming}
              onClick={() => onConfirmSend?.()}
              data-testid="mail-confirm-send"
            >
              {confirming ? '提交中…' : '确认发送'}
            </button>
          </div>
        ) : null}
      </div>
    </div>
  )
}
