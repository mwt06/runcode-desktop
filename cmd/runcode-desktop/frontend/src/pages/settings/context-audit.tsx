// 上下文审核（仅测试版构建）：完全自包含，读写直接落后端。正式版后端报
// supported=false，整节不渲染——功能对正式用户不存在，而不是灰掉。
import { useEffect, useState } from 'react'
import { BTN } from '@/ui/tokens'
import { contextAuditStatus, copyText, errText, openInBrowser, setContextAudit, type ContextAuditInfo } from '@/core/bridge'
import { Toggle } from '@/ui/toggle'
import { Section } from './section'

export function ContextAuditSection() {
  const [info, setInfo] = useState<ContextAuditInfo | null>(null)
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  useEffect(() => {
    contextAuditStatus().then(setInfo).catch(() => {})
  }, [])
  if (!info?.supported) return null

  async function toggle(next: boolean) {
    setBusy(true)
    setMsg('')
    try {
      setInfo(await setContextAudit(next))
    } catch (e) {
      setMsg(errText(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Section title="上下文审核" hint="测试版专属">
      <label className="flex items-center justify-between gap-3 cursor-pointer">
        <span className="min-w-0">
          <span className="text-[13px]">开启上下文审核</span>
          <span className="block text-[12px] text-muted mt-0.5">
            记录每次发给模型的完整请求上下文（系统提示词、全部消息、工具清单），按会话落盘，对运行中的会话即时生效。图片只记尺寸不存原图。
          </span>
        </span>
        <Toggle on={info.enabled} onChange={toggle} disabled={busy} />
      </label>
      {info.enabled && info.url && (
        <div className="flex items-center gap-2">
          <code className="flex-1 min-w-0 font-mono text-[12.5px] text-muted bg-bg border border-line2 rounded-[8px] px-3 py-1.5 overflow-hidden text-ellipsis whitespace-nowrap">{info.url}</code>
          <button type="button" className={`${BTN} px-4 flex-none`} onClick={() => openInBrowser(info.url)}>打开查看页</button>
          <button type="button" className={`${BTN} px-4 flex-none`} onClick={async () => { await copyText(info.url); setMsg('地址已复制'); setTimeout(() => setMsg(''), 2000) }}>复制</button>
        </div>
      )}
      {msg && <div className="text-[12px] text-muted -mt-1">{msg}</div>}
      <p className="text-[11.5px] text-faint -mt-1">
        记录目录：<span className="font-mono break-all">{info.dir || '—'}</span>。审核记录包含完整提示词与对话内容，仅保存在本机、仅限本机页面查看；测试完可直接删除该目录。
      </p>
    </Section>
  )
}
