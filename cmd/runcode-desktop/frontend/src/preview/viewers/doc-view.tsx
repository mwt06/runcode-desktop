// docx 与 pptx 都是命令式渲染(各自的库自己生成 DOM)，所以共用 ImperativeDocView。
// 渲染器在 load 函数内部懒加载，把重依赖(docx-preview;pptx-preview 还会拖进
// echarts)挡在主包之外——只有真的预览到这类文件时才下载。load 定义在模块级，
// 保证跨渲染的引用稳定(它是 effect 依赖)。
import { useEffect, useRef, useState } from 'react'
import { errText, readArtifactBytes } from '@/core/bridge'
import { ViewerError } from './viewer-error'

export async function renderDocx(buf: ArrayBuffer, host: HTMLDivElement) {
  const docx = await import('docx-preview')
  await docx.renderAsync(buf, host, host, {
    className: 'docx',
    inWrapper: true,
    breakPages: true,
    renderHeaders: true,
    renderFooters: true,
    experimental: true,
    useBase64URL: true,
  })
}

// ImperativeDocView fetches a workspace file's bytes and hands them to a renderer
// that draws into a host node, owning the loading / error shell and an
// open-externally fallback. It re-runs when the file or the refresh key changes and
// cancels cleanly so a stale render never lands in the wrong file's host.
export function ImperativeDocView({ relPath, reloadKey, load, busyHint }: {
  relPath: string
  reloadKey: number
  load: (buf: ArrayBuffer, host: HTMLDivElement) => Promise<void>
  busyHint: string
}) {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [err, setErr] = useState('')
  useEffect(() => {
    let cancelled = false
    setState('loading')
    setErr('')
    void (async () => {
      try {
        const buf = await readArtifactBytes(relPath)
        const host = hostRef.current
        if (cancelled || !host) return
        host.replaceChildren() // drop any prior file's render before drawing this one
        await load(buf, host)
        if (!cancelled) setState('ready')
      } catch (e) {
        if (!cancelled) {
          setErr(errText(e))
          setState('error')
        }
      }
    })()
    return () => { cancelled = true }
  }, [relPath, reloadKey, load])
  return (
    <div className="min-h-full bg-inset/40 flex flex-col items-center">
      {state === 'loading' && <div className="p-6 text-[13px] text-muted">正在加载{busyHint}预览…</div>}
      {state === 'error' && <ViewerError relPath={relPath} message={err} />}
      {/* Host stays mounted (not hidden) so a renderer that measures its width — pptx —
          sees the real laid-out width rather than 0. flex + items-center 让固定宽度的
          docx 页面在面板里水平居中（窄于面板时居中，宽时正常横向滚动）。 */}
      <div ref={hostRef} className="docview-host" />
    </div>
  )
}
