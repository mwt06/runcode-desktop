// ChatGPT(Codex)登录：设备码流程的那一行。
//
// 流程天然是两步——先拿到一串码给用户看，用户在浏览器里输完，客户端才换到令牌
// ——所以后端也是两条命令：codexStartLogin 取码（顺手开浏览器），codexAwaitLogin
// 一直等到授权完成。这里把这两步串起来，并把码显示在原地，不弹新窗口。
import { useEffect, useState } from 'react'
import { BTN } from '@/ui/tokens'
import { InlineError } from '@/ui/feedback'
import {
  codexAwaitLogin,
  codexCancelLogin,
  codexLogout,
  codexModels,
  codexStartLogin,
  codexStatus,
  copyText,
  errText,
  openExternal,
  type CodexDeviceCode,
  type CodexModel,
  type CodexStatus,
} from '@/core/bridge'

// onModels 把上游给出的可用模型报给表单：模型 ID 只能是这里面的，凭印象填一个
// 上游不认的名字换来的是一句没法自查的 400（实测 gpt-5-codex 就是这样被拒的）。
export function CodexLoginRow({ onModels }: { onModels?: (models: CodexModel[]) => void }) {
  const [status, setStatus] = useState<CodexStatus | null>(null)
  const [code, setCode] = useState<CodexDeviceCode | null>(null)
  const [waiting, setWaiting] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    codexStatus().then((s) => {
      setStatus(s ?? null)
      if (s?.loggedIn) void loadModels()
    }).catch(() => {})
    // loadModels 只用到稳定的 setter 与 onModels；把它列进依赖会让这个
    // "开页拉一次"的效果随父级每次渲染重跑。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const loadModels = async () => {
    try {
      onModels?.((await codexModels()) ?? [])
    } catch {
      // 拉不到清单不影响登录本身：表单退回让用户自己填模型 ID。
    }
  }

  const login = async () => {
    setError('')
    try {
      const dc = await codexStartLogin()
      setCode(dc ?? null)
      setWaiting(true)
      // 这一等会持续到用户在浏览器里点完（或码过期）。期间码一直显示在上面，
      // 用户随时能再看一眼要输什么。
      const next = await codexAwaitLogin()
      setStatus(next ?? null)
      setCode(null)
      void loadModels()
    } catch (e) {
      setError(errText(e))
    } finally {
      setWaiting(false)
    }
  }

  const cancel = async () => {
    try { await codexCancelLogin() } catch { /* 取消失败无所谓：状态已经收掉了 */ }
    setCode(null)
    setWaiting(false)
  }

  if (status?.loggedIn) {
    return (
      <div className="flex items-center justify-between rounded-field border border-line2 bg-surface2 px-3 py-2 text-[13px]">
        <span className="truncate">
          已登录 ChatGPT
          {status.email && <span className="text-muted"> · {status.email}</span>}
        </span>
        <button
          type="button"
          className="text-[12px] text-muted hover:text-red flex-none ml-2"
          onClick={async () => { setStatus((await codexLogout()) ?? null); onModels?.([]) }}
        >
          退出登录
        </button>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-2 rounded-field border border-line2 bg-surface2 px-3 py-2.5">
      {code ? (
        <>
          <div className="text-[12px] text-muted">
            在打开的页面里输入下面这串码完成授权（没自动打开就点「重新打开页面」）：
          </div>
          <div className="flex items-center gap-2">
            <span className="font-mono text-[18px] tracking-widest text-ink select-all">{code.userCode}</span>
            <button type="button" className="text-[12px] text-muted hover:text-primaryink" onClick={() => void copyText(code.userCode)}>复制</button>
            <button type="button" className="text-[12px] text-muted hover:text-primaryink" onClick={() => void openExternal(code.verificationUrl)}>重新打开页面</button>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-[12px] text-faint">{waiting ? '等待浏览器里完成授权…' : '已停止等待'}</span>
            <button type="button" className="text-[12px] text-muted hover:text-ink" onClick={() => void cancel()}>取消</button>
          </div>
        </>
      ) : (
        <div className="flex items-center justify-between">
          <span className="text-[12px] text-muted">用 ChatGPT 订阅授权后即可使用 Codex，无需 API 密钥。</span>
          <button type="button" className={`${BTN} px-4 py-1.5 text-[12px] flex-none ml-2`} disabled={waiting} onClick={() => void login()}>
            {waiting ? '登录中…' : '登录 ChatGPT'}
          </button>
        </div>
      )}
      {error && <InlineError variant="text">{error}</InlineError>}
    </div>
  )
}
