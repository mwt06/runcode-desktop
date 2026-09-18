// sudo 密码框：模型要以管理员身份执行一条命令，用户在这里输系统密码。
//
// 这是第二道关（第一道是先前那次命令审批），所以文案要把三件事说清楚：
//
//   1. **要执行的是什么**——显示的是 sudo 进程**实际**的命令行（后端从它自己的 /proc
//      读出来），不是审批时那段文本。两者本该一致；万一不一致，用户在交出密码之前
//      看到的仍是真相。
//   2. **密码交给谁**——只交给 sudo，模型看不到。
//   3. **不想给就取消**——sudo 会报"没有提供密码"，模型会看到这句，不会卡住。
//
// 密码只活在本组件的 state 里，提交或取消后组件卸载，它随之消失；不落任何持久化。
import { useEffect, useRef, useState } from 'react'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { isComposingKey } from '@/ui/keys'
import { type AskpassRequest } from '@/core/bridge'

export function AskpassModal({ req, remaining = 0, onAnswer, onCancel }: {
  req: AskpassRequest
  remaining?: number
  onAnswer: (password: string) => void
  onCancel: () => void
}) {
  const [password, setPassword] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  // 换了一个请求就清空并重新聚焦：不能把上一次输的密码带给下一条命令。
  useEffect(() => {
    setPassword('')
    inputRef.current?.focus()
  }, [req.id])

  const submit = () => {
    if (!password) return
    onAnswer(password)
    setPassword('')
  }

  return (
    <div className="fixed inset-0 bg-[rgba(30,33,50,0.32)] backdrop-blur-[2px] flex items-center justify-center z-30 anim-rise">
      <div className="w-[520px] max-w-[92vw] bg-surface rounded-2xl p-[22px] shadow-modal">
        <h3 className="m-0 mb-3.5 text-[16px] font-bold flex items-center gap-2.5">
          <span className="w-[9px] h-[9px] rounded-[3px] bg-amber" />需要管理员权限
          {remaining > 0 && (
            <span className="ml-auto text-[12px] font-medium text-muted bg-surface2 border border-line2 rounded-full px-2.5 py-0.5">还有 {remaining} 个待处理</span>
          )}
        </h3>

        <div className="text-[13px] text-ink mb-2">模型要以管理员身份执行：</div>
        <pre className="m-0 mb-3.5 font-mono text-[12px] text-ink bg-surface2 border border-line2 rounded-lg px-3 py-2.5 whitespace-pre-wrap break-all max-h-40 overflow-auto">{req.command}</pre>

        <label className="block text-[13px] text-ink mb-1.5" htmlFor="askpass-input">请输入本机登录密码</label>
        <input
          id="askpass-input"
          ref={inputRef}
          type="password"
          autoComplete="off"
          spellCheck={false}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          onKeyDown={(e) => {
            if (isComposingKey(e)) return
            if (e.key === 'Enter') { e.preventDefault(); submit() }
            if (e.key === 'Escape') { e.preventDefault(); onCancel() }
          }}
          className="w-full h-9 px-3 text-[13px] bg-surface border border-line2 rounded-lg outline-none focus:border-primary"
        />
        <div className="text-[12px] text-muted mt-2">
          密码只交给系统的 sudo，模型看不到它。不想授权就点「取消」，这条命令会以失败告终。
        </div>

        <div className="flex justify-end gap-2 mt-4">
          <button type="button" className={`${BTN} px-4`} onClick={onCancel}>取消</button>
          <button type="button" className={`${BTN} ${BTN_PRIMARY} px-5`} disabled={!password} onClick={submit}>授权</button>
        </div>
      </div>
    </div>
  )
}
