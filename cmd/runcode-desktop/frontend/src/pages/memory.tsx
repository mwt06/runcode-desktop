// MemoryPage shows the two forms of persistent context: the workspace's project
// instructions (CLAUDE.md / RUNCODE.md, editable) and the agent's memory (read-only
// — the model maintains it via its memory tool).
import { useEffect, useState } from 'react'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { errText, readMemory, readProjectContext, saveProjectContext, type MemoryInfo, type ProjectContextInfo } from '@/core/bridge'

export function MemoryPage() {
  const [ctx, setCtx] = useState<ProjectContextInfo | null>(null)
  const [content, setContent] = useState('')
  const [mem, setMem] = useState<MemoryInfo>({ user: [], project: [] })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    void (async () => {
      try {
        const [c, m] = await Promise.all([readProjectContext(), readMemory()])
        setCtx(c)
        setContent(c?.content ?? '')
        setMem({ user: m?.user ?? [], project: m?.project ?? [] })
      } catch (e) {
        setError(errText(e))
      } finally {
        setLoading(false)
      }
    })()
  }, [])

  async function save() {
    setSaving(true)
    setError('')
    try {
      await saveProjectContext(content)
      setCtx((c) => (c ? { ...c, content, exists: true } : c))
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
    } catch (e) {
      setError(errText(e))
    } finally {
      setSaving(false)
    }
  }

  const hasWorkspace = !!ctx && ctx.path !== ''
  const dirty = !!ctx && content !== ctx.content
  const memSection = (title: string, note: string, raw: string[] | null | undefined) => {
    const entries = raw ?? []
    return (
    <div>
      <div className="flex items-baseline gap-2 mb-1.5">
        <span className="text-[13px] font-semibold text-ink">{title}</span>
        <span className="text-[12px] text-faint">{entries.length}</span>
        <span className="text-[11.5px] text-faint ml-auto">{note}</span>
      </div>
      {entries.length === 0 ? (
        <div className="text-faint text-[12.5px] py-2">暂无</div>
      ) : (
        <ul className="flex flex-col gap-1.5 m-0 p-0 list-none">
          {entries.map((e, i) => (
            <li key={i} className="text-[12.5px] text-ink bg-surface2 border border-line2 rounded-lg px-3 py-2 leading-relaxed break-words">{e}</li>
          ))}
        </ul>
      )}
    </div>
    )
  }

  return (
    <div className="flex-1 overflow-y-auto px-[22px] py-7">
      <div className="max-w-[720px] mx-auto flex flex-col gap-5">
        <div>
          <h2 className="m-0 text-[20px] font-bold tracking-tight">记忆与项目指令</h2>
          <p className="mt-1 text-muted text-[13px]">项目指令随每次对话注入给模型;记忆由模型在跨会话时自行记录。</p>
        </div>

        {error && <div className="text-red text-[13px] bg-redbg border border-red/25 rounded-lg px-3 py-2.5 whitespace-pre-wrap break-words">{error}</div>}

        {loading ? (
          <div className="text-muted text-[13px] py-6 text-center">加载中…</div>
        ) : (
          <>
            <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-3 shadow-xs">
              <div className="flex items-center gap-2">
                <span className="text-[14px] font-semibold text-ink">项目指令</span>
                {ctx && <span className="font-mono text-[11.5px] text-faint bg-surface2 border border-line2 rounded px-1.5 py-0.5">{ctx.name}{!ctx.exists ? ' · 新建' : ''}</span>}
                <div className="ml-auto flex items-center gap-2">
                  {saved && <span className="text-[12px] text-green">已保存</span>}
                  <button className={`${BTN} ${BTN_PRIMARY} px-3 py-1.5 text-[13px]`} disabled={!hasWorkspace || !dirty || saving} onClick={save}>{saving ? '保存中…' : '保存'}</button>
                </div>
              </div>
              {hasWorkspace ? (
                <textarea
                  className="font-mono text-[12.5px] leading-relaxed bg-surface2 text-ink border border-line2 rounded-[9px] px-3 py-2.5 outline-none focus:border-primary focus:shadow-[0_0_0_3px_var(--color-primarysoft)] min-h-[240px] resize-y"
                  value={content}
                  onChange={(e) => setContent(e.target.value)}
                  placeholder="# 项目说明&#10;&#10;写下让 AI 遵守的项目约定、技术栈、协作原则……每次对话都会带上这些。"
                  spellCheck={false}
                />
              ) : (
                <div className="text-faint text-[13px] py-4 text-center">启动一个会话后可编辑项目指令。</div>
              )}
            </section>

            <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-4 shadow-xs">
              <div className="text-[14px] font-semibold text-ink">记忆</div>
              {memSection('用户记忆', '全局 · 跨项目', mem.user)}
              {memSection('项目记忆', '仅本工作区', mem.project)}
              <div className="text-[11.5px] text-faint">记忆由模型通过记忆工具自动维护;如需修改,可在对话中让它更新。</div>
            </section>
          </>
        )}
      </div>
    </div>
  )
}
