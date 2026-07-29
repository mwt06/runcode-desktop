// composer 是对话输入区：多行输入框、@技能 / /子代理 / #文件 三种触发的选择器、
// 图片附件，以及底部工具条(拆到 ./toolbar)。选择器与附件等 UI 状态全部内化；会话
// 状态的变更（切模式/切模型/发送/停止）一律经 on* 回调交还 App——本组件从不直接
// 改会话状态。
import { useEffect, useRef, useState, type RefObject } from 'react'
import { Icon } from '@/ui/icons'
import { basename } from '@/core/paths'
import { pickImageAttachment, listAgents, listSkills, type AgentInfo, type SessionInfo, type SkillInfo } from '@/core/bridge'
import { type ModelOption } from '@/ui/model-picker'
import { composerKeyAction } from './keymap'
import { applyMention, computeMention, matchByNameOrDesc, rankFileMatches, type MentionTrigger } from './mention'
import { AgentPicker, FilePicker, SkillPicker } from './mention-picker'
import { ComposerToolbar } from './toolbar'

// Composer 的对外契约：input 受控于 App（插件页“使用技能/委派子代理”要往输入框
// 追加文案并聚焦，故 input 与 taRef 由 App 持有）；files 归 App（文件浏览器/回复
// 产物条也要用），选择器打开时经 onRefreshFiles 请求刷新。
export function Composer({
  input,
  onInputChange,
  taRef,
  busy,
  toast,
  info,
  files,
  sessionId,
  onRefreshFiles,
  onSend,
  onStop,
  onToggleMode,
  onTogglePlan,
  onChooseReasoning,
  onChooseThinking,
  onPickModel,
}: {
  input: string
  onInputChange: (v: string) => void
  taRef: RefObject<HTMLTextAreaElement>
  busy: boolean
  toast: string
  info: SessionInfo | null
  files: string[]
  sessionId?: string
  onRefreshFiles: () => void
  onSend: (text: string, attachments: string[]) => void
  onStop: () => void
  onToggleMode: () => void
  onTogglePlan: () => void
  onChooseReasoning: (scenario: string) => void
  onChooseThinking: (effort: string) => void
  onPickModel: (choice: ModelOption) => Promise<void> | void
}) {
  const [mention, setMention] = useState<{ query: string; start: number; sel: number; trigger: MentionTrigger } | null>(null)
  const [attachments, setAttachments] = useState<string[]>([])
  const [chatSkills, setChatSkills] = useState<SkillInfo[]>([])
  const [chatAgents, setChatAgents] = useState<AgentInfo[]>([])
  const selItemRef = useRef<HTMLDivElement>(null)

  // Keep the highlighted picker item visible as the selection moves with ↑/↓.
  useEffect(() => {
    selItemRef.current?.scrollIntoView({ block: 'nearest' })
  }, [mention?.sel])

  // Composer pickers: skills (@) and sub-agents (/) — refreshed per session.
  useEffect(() => {
    listSkills()
      .then((l) => setChatSkills(l?.skills ?? []))
      .catch(() => setChatSkills([]))
    listAgents()
      .then((l) => setChatAgents(l?.agents ?? []))
      .catch(() => setChatAgents([]))
  }, [sessionId])

  const fileMatches = mention?.trigger === '#' ? rankFileMatches(files, mention.query) : []
  const skillMatches = mention?.trigger === '@' ? matchByNameOrDesc(chatSkills, mention.query) : []
  // Only user/project sub-agents are offered here; built-ins are hidden from the
  // picker (the model can still delegate to them on its own).
  const agentMatches = mention?.trigger === '/' ? matchByNameOrDesc(chatAgents.filter((a) => a.source !== 'builtin'), mention.query) : []
  const mentionCount =
    mention?.trigger === '@' ? skillMatches.length : mention?.trigger === '/' ? agentMatches.length : fileMatches.length

  function syncMention(value: string, cursor: number) {
    const m = computeMention(value, cursor)
    setMention(m ? { ...m, sel: 0 } : null)
  }

  // pick splices the chosen item over the trigger and drops the picker.
  function pick(picked: string) {
    if (!mention) return
    const { value, caret } = applyMention(input, mention, picked)
    onInputChange(value)
    setMention(null)
    setCaret(caret)
  }

  // pickHighlighted applies the highlighted item for the active trigger.
  function pickHighlighted(index: number) {
    if (!mention) return
    const list: (string | undefined)[] =
      mention.trigger === '@' ? skillMatches.map((s) => s.name)
      : mention.trigger === '/' ? agentMatches.map((a) => a.name)
      : fileMatches
    const picked = list[Math.min(index, list.length - 1)]
    if (picked) pick(picked)
  }

  function setCaret(pos: number) {
    requestAnimationFrame(() => {
      const ta = taRef.current
      if (ta) {
        ta.focus()
        ta.setSelectionRange(pos, pos)
      }
    })
  }

  function openFilePicker() {
    const ta = taRef.current
    const sep = input && !/\s$/.test(input) ? ' ' : ''
    const next = input + sep + '#'
    onInputChange(next)
    setMention({ query: '', start: next.length - 1, sel: 0, trigger: '#' })
    // Refresh the workspace file list on open — a session that listed none at
    // startup (or before its workspace was ready) still gets a current list.
    onRefreshFiles()
    requestAnimationFrame(() => ta?.focus())
  }
  // The "+" menu opens the skill (@) or sub-agent (/) picker directly, mirroring
  // typing the trigger at the composer start. Both are start-of-input commands that
  // replace the whole input on pick, so each opens a fresh one.
  function openTriggerPicker(trigger: '@' | '/') {
    onInputChange(trigger)
    setMention({ query: '', start: 0, sel: 0, trigger })
    setCaret(1)
  }
  // Pick an image from disk and attach it to the next message. The path is kept in
  // `attachments` and the bytes are read backend-side at send time.
  async function pickAttachment() {
    try {
      const p = await pickImageAttachment()
      if (p) setAttachments((a) => (a.includes(p) ? a : [...a, p]))
    } catch {
      /* cancelled or unavailable */
    }
  }

  // 忙时不再拦截:onSend 会把消息排进补充队列(回合结束后自动发)。所以这里发/排队
  // 走同一条路,由下游按 busy 决定,组件不必自己分叉。
  function handleSend() {
    const text = input.trim()
    if (!text && attachments.length === 0) return
    const attach = attachments
    onInputChange('')
    setAttachments([])
    onSend(text, attach)
  }

  const hoverMention = (i: number) => setMention((m) => (m ? { ...m, sel: i } : m))

  return (
    <footer className="flex-none relative px-6 pt-3.5 pb-[18px] w-full max-w-[1200px] mx-auto">
      {toast && (
        <div className="absolute left-0 right-0 -top-1 flex justify-center pointer-events-none z-10">
          <div className="anim-rise px-3 py-1.5 rounded-full text-[12px] bg-surface2 border border-line2 text-muted shadow-card">
            {toast}
          </div>
        </div>
      )}
      {info?.planMode && (
        <div className="flex items-center gap-2.5 mb-2 bg-primarysoft border border-primary rounded-[12px] px-3.5 py-2.5">
          <span className="text-primaryink flex-none"><Icon name="compass" size={16} /></span>
          <span className="text-[12.5px] text-primaryink flex-1 min-w-0">计划模式：只调研、产出方案，不会修改任何文件。方案给出后可选择如何继续。</span>
        </div>
      )}
      {mention?.trigger === '@' && skillMatches.length > 0 && (
        <SkillPicker items={skillMatches} sel={mention.sel} selRef={selItemRef} onHover={hoverMention} onPick={pick} />
      )}
      {mention?.trigger === '/' && agentMatches.length > 0 && (
        <AgentPicker items={agentMatches} sel={mention.sel} selRef={selItemRef} onHover={hoverMention} onPick={pick} />
      )}
      {mention?.trigger === '#' && (fileMatches.length > 0 || mention.query === '') && (
        <FilePicker items={fileMatches} sel={mention.sel} selRef={selItemRef} onHover={hoverMention} onPick={pick} />
      )}
      {attachments.length > 0 && (
        <div className="flex flex-wrap gap-2 mb-1.5">
          {attachments.map((p, i) => (
            <span key={p + i} className="inline-flex items-center gap-1.5 bg-surface2 border border-line2 rounded-[9px] pl-2.5 pr-1 py-1 text-[12px] text-ink max-w-[220px]">
              <Icon name="file" size={13} />
              <span className="truncate">{basename(p)}</span>
              <button className="flex-none text-faint hover:text-red px-1 leading-none" title="移除附件" onClick={() => setAttachments((a) => a.filter((x) => x !== p))}>✕</button>
            </span>
          ))}
        </div>
      )}
      <textarea
        ref={taRef}
        className="block w-full resize-none min-h-[46px] max-h-[200px] bg-surface text-ink border border-line2 border-b-0 rounded-t-[14px] px-4 py-3.5 outline-none placeholder:text-faint"
        value={input}
        placeholder={busy ? '回合进行中——输入后 Enter 立即补充，模型会在当前步骤结束后看到' : '继续对话，@ 技能，/ 子代理，# 文件；Enter 发送，Shift+Enter 换行'}
        onChange={(e) => {
          onInputChange(e.target.value)
          syncMention(e.target.value, e.target.selectionStart ?? e.target.value.length)
        }}
        onClick={(e) => syncMention(input, e.currentTarget.selectionStart ?? input.length)}
        onKeyUp={(e) => {
          if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) {
            syncMention(input, e.currentTarget.selectionStart ?? input.length)
          }
        }}
        onKeyDown={(e) => {
          // 按键归属见 keymap（纯函数，含输入法与 Shift+Enter 的用例）。'default'
          // 就是不拦，换行、光标移动等原生行为都走这条路。
          const action = composerKeyAction(e, !!mention && mentionCount > 0)
          if (action.kind === 'default') return
          e.preventDefault()
          switch (action.kind) {
            case 'movePicker':
              setMention((m) => (m ? { ...m, sel: Math.min(Math.max(m.sel + action.delta, 0), mentionCount - 1) } : m))
              break
            case 'pickHighlighted':
              if (mention) pickHighlighted(mention.sel)
              break
            case 'closePicker':
              setMention(null)
              break
            case 'send':
              handleSend()
              break
          }
        }}
      />
      <ComposerToolbar
        info={info}
        busy={busy}
        canSend={!!input.trim() || attachments.length > 0}
        onOpenSkillPicker={() => openTriggerPicker('@')}
        onOpenAgentPicker={() => openTriggerPicker('/')}
        onOpenFilePicker={openFilePicker}
        onPickAttachment={() => void pickAttachment()}
        onToggleMode={onToggleMode}
        onTogglePlan={onTogglePlan}
        onChooseReasoning={onChooseReasoning}
        onChooseThinking={onChooseThinking}
        onPickModel={onPickModel}
        onSend={handleSend}
        onStop={onStop}
      />
    </footer>
  )
}
