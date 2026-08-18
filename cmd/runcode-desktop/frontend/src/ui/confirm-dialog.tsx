import { type ReactNode } from 'react'
import { BTN, BTN_DANGER } from './tokens'

// ConfirmDialog is an in-app confirmation modal in the app's own style — replaces
// the browser's native window.confirm(), which renders the ugly "wails.localhost
// 显示" chrome. Backdrop click and 取消 both dismiss; 确认 runs onConfirm.
export function ConfirmDialog({ title, message, confirmLabel, onConfirm, onCancel }: {
  title: string
  message: ReactNode
  confirmLabel: string
  onConfirm: () => void
  onCancel: () => void
}) {
  return (
    <div className="fixed inset-0 bg-[rgba(30,33,50,0.32)] backdrop-blur-[2px] flex items-center justify-center z-30 anim-rise" onClick={onCancel}>
      <div className="w-[400px] max-w-[92vw] bg-surface rounded-2xl p-[22px] shadow-modal" onClick={(e) => e.stopPropagation()}>
        <h3 className="m-0 mb-2.5 text-[16px] font-bold flex items-center gap-2.5">
          <span className="w-[9px] h-[9px] rounded-[3px] bg-red" />{title}
        </h3>
        <div className="text-[14px] text-muted leading-relaxed break-words">{message}</div>
        <div className="mt-5 flex justify-end gap-2.5">
          <button type="button" onClick={onCancel} className={`${BTN} px-5`}>取消</button>
          <button type="button" autoFocus onClick={onConfirm} className={`${BTN} ${BTN_DANGER} px-5`}>{confirmLabel}</button>
        </div>
      </div>
    </div>
  )
}
