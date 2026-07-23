// composer 是对话输入区：多行输入框、@技能 / /子代理 / #文件 三种触发的选择器、
// 图片附件、以及工具条（+ 菜单、权限模式、计划模式、思考强度、模型选择、发送/停止）。
// 选择器与附件等 UI 状态全部内化；会话状态的变更（切模式/切模型/发送/停止）一律
// 经 on* 回调交还 App——本组件从不直接改会话状态。从 App.tsx 搬出，行为不变。
import { useEffect, useRef, useState, type RefObject } from 'react'
import { Icon } from '@/ui/icons'
import {
  listAgents,
  listCustomModels,
  listSkills,
  pickImageAttachment,
  sessionModels,
  type AgentInfo,
  type SessionInfo,
  type SkillInfo,
} from '@/core/bridge'
import { ModelPickerPopover, type ModelOption } from '@/ui/model-picker'
import { Popover } from '@/ui/popover'
import { SourceBadge } from '@/ui/badges'
import { GhostBtn } from '@/ui/ghost-btn'
import { classifyPreview, fileColor, kindIcon } from '@/preview/classify'
import { basename } from '@/core/paths'
import { customModelOptionSub } from '@/core/custom-models'

const MODE_LABEL: Record<string, string> = { safe: '安全模式', interactive: '交互模式', judge: '智能模式', flight: '飞行模式' }
// "Thinking model" options for the in-conversation picker.
const REASONING: { value: string; label: string }[] = [
  { value: 'off', label: '不启用' },
  { value: 'auto', label: '自动分类（每轮多一次调用）' },
  { value: 'troubleshooting', label: '排障 · 5 Whys' },
  { value: 'proposal', label: '方案 · 金字塔原理' },
  { value: 'architecture', label: '架构 · 第一性原理' },
  { value: 'project_management', label: '项目 · 闭环 + 80/20' },
  { value: 'incident_response', label: '救火 · OODA' },
  { value: 'general', label: '通用 · 10 步清单' },
]
const REASONING_LABEL: Record<string, string> = Object.fromEntries(REASONING.map((r) => [r.value, r.label]))

// 思考模型（reasoning scenario）按钮暂时隐藏；改回 true 即可恢复整块 UI 与逻辑。
const SHOW_THINKING_MODEL = false

// "Thinking strength" options: provider-native reasoning effort (OpenAI
// reasoning_effort / an Anthropic thinking budget). This is the knob that makes a
// reasoning model actually emit the reasoning content shown above each answer.
const THINKING: { value: string; label: string }[] = [
  { value: 'off', label: '不启用' },
  { value: 'low', label: '低 · 快速' },
  { value: 'medium', label: '中 · 均衡' },
  { value: 'high', label: '高 · 深度' },
]
const THINKING_LABEL: Record<string, string> = { low: '低', medium: '中', high: '高' }

export type MentionTrigger = '@' | '/' | '#'
// computeMention finds an active mention/command trigger ending at the cursor:
//   '@' (a skill command) and '/' (a sub-agent command) only at the very start;
//   '#' (a file mention) at the start or after whitespace, anywhere in the input.
// In every case there must be no whitespace between the trigger and the cursor.
export function computeMention(value: string, cursor: number): { query: string; start: number; trigger: MentionTrigger } | null {
  const upToCursor = value.slice(0, cursor)
  if (value.startsWith('@') && !/\s/.test(upToCursor.slice(1))) {
    return { query: upToCursor.slice(1), start: 0, trigger: '@' }
  }
  if (value.startsWith('/') && !/\s/.test(upToCursor.slice(1))) {
    return { query: upToCursor.slice(1), start: 0, trigger: '/' }
  }
  const hash = upToCursor.lastIndexOf('#')
  if (hash < 0) return null
  if (hash > 0 && !/\s/.test(value[hash - 1])) return null
  const query = upToCursor.slice(hash + 1)
  if (/\s/.test(query)) return null
  return { query, start: hash, trigger: '#' }
}

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
  const [reasonMenu, setReasonMenu] = useState(false)
  const [thinkMenu, setThinkMenu] = useState(false)
  const [addMenu, setAddMenu] = useState(false)
  const [attachments, setAttachments] = useState<string[]>([])
  const [chatSkills, setChatSkills] = useState<SkillInfo[]>([])
  const [chatAgents, setChatAgents] = useState<AgentInfo[]>([])
  const selItemRef = useRef<HTMLDivElement>(null)
  // 对话内模型选择器：点底部模型名弹出，模糊检索，最多显示 10 个。平台(通行证)模型
  // 与自定义直连模型合并展示，都能被搜索和切换；切换本身（换连接/重建会话）由
  // App 的 onPickModel 完成。
  const [modelPickerOpen, setModelPickerOpen] = useState(false)
  const [modelOptions, setModelOptions] = useState<ModelOption[]>([])
  const openModelPicker = async () => {
    setModelPickerOpen(true)
    try {
      const [platform, custom] = await Promise.all([
        sessionModels().catch(() => null),
        listCustomModels().catch(() => null),
      ])
      // id 即 SwitchModel 的入参——平台模型传模型 id，自定义模型传其显示名；
      // modelId 标记当前选中项(自定义模型落在 info.model 里的是底层模型 id)。
      setModelOptions([
        ...(platform ?? []).map((m): ModelOption => ({ kind: 'platform', id: m.id, label: m.id, sub: m.ownedBy })),
        ...(custom ?? []).map((c): ModelOption => ({ kind: 'custom', id: c.name, label: c.name, sub: customModelOptionSub(c), modelId: c.model })),
      ])
    } catch { setModelOptions([]) }
  }
  // 菜单先收起再回调 App——与拆分前 chooseReasoning/chooseThinking 的时序一致。
  const chooseReasoning = (scenario: string) => {
    setReasonMenu(false)
    onChooseReasoning(scenario)
  }
  const chooseThinking = (effort: string) => {
    setThinkMenu(false)
    onChooseThinking(effort)
  }

  // Composer toolbar responsiveness. The bar's width depends on the sidebar and
  // preview pane, not the viewport, so a CSS media query can't see it — and a CSS
  // container query is unusable here because container-type applies layout
  // containment, which would trap the menus' `fixed inset-0` click-away overlays
  // inside the bar. So measure the bar itself and drop labels to icons as it
  // narrows (tooltips and the active-state colors still convey each button).
  const toolbarRef = useRef<HTMLDivElement | null>(null)
  const [toolbarW, setToolbarW] = useState(0)
  useEffect(() => {
    const el = toolbarRef.current
    if (!el) {
      setToolbarW(0)
      return
    }
    const ro = new ResizeObserver(([e]) => setToolbarW(e.contentRect.width))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  // Thresholds are the bar's natural content width: ~660px with every label, ~440px
  // once the secondary labels are icons. 0 = not measured yet → stay expanded.
  const compactBar = toolbarW > 0 && toolbarW < 660
  const tinyBar = toolbarW > 0 && toolbarW < 450

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

  // Files matching the #-query (basename/prefix ranked) — only for the '#' trigger.
  const fileMatches =
    mention?.trigger === '#'
      ? (() => {
          const q = mention.query.toLowerCase()
          const hits = q ? files.filter((p) => p.toLowerCase().includes(q)) : files
          return [...hits]
            .sort((a, b) => {
              const ba = a.slice(a.lastIndexOf('/') + 1).toLowerCase()
              const bb = b.slice(b.lastIndexOf('/') + 1).toLowerCase()
              const sa = q && ba.startsWith(q) ? 0 : 1
              const sb = q && bb.startsWith(q) ? 0 : 1
              return sa - sb || a.length - b.length
            })
            .slice(0, 50)
        })()
      : []
  // Skills matching the @-query (by name or description) — only for the '@' trigger.
  const skillMatches =
    mention?.trigger === '@'
      ? (() => {
          const q = mention.query.toLowerCase()
          return chatSkills.filter((s) => !q || s.name.toLowerCase().includes(q) || s.description.toLowerCase().includes(q)).slice(0, 50)
        })()
      : []
  // Sub-agents matching the /-query (by name or description) — only for '/'.
  const agentMatches =
    mention?.trigger === '/'
      ? (() => {
          const q = mention.query.toLowerCase()
          // Only user/project sub-agents are offered here; built-ins are hidden
          // from the picker (the model can still delegate to them on its own).
          return chatAgents.filter((a) => a.source !== 'builtin').filter((a) => !q || a.name.toLowerCase().includes(q) || a.description.toLowerCase().includes(q)).slice(0, 50)
        })()
      : []
  const mentionCount =
    mention?.trigger === '@' ? skillMatches.length : mention?.trigger === '/' ? agentMatches.length : fileMatches.length

  function syncMention(value: string, cursor: number) {
    const m = computeMention(value, cursor)
    setMention(m ? { ...m, sel: 0 } : null)
  }

  // pickMention applies the highlighted item for the active trigger.
  function pickMention(index: number) {
    if (!mention) return
    if (mention.trigger === '@') {
      const sk = skillMatches[Math.min(index, skillMatches.length - 1)]
      if (sk) pickSkill(sk.name)
    } else if (mention.trigger === '/') {
      const ag = agentMatches[Math.min(index, agentMatches.length - 1)]
      if (ag) pickAgent(ag.name)
    } else {
      const path = fileMatches[Math.min(index, fileMatches.length - 1)]
      if (path) pickFile(path)
    }
  }

  function pickFile(path: string) {
    if (!mention) return
    const before = input.slice(0, mention.start)
    const after = input.slice(mention.start + 1 + mention.query.length)
    const insert = '#' + path + ' '
    onInputChange(before + insert + after)
    setMention(null)
    setCaret((before + insert).length)
  }

  function pickSkill(name: string) {
    if (!mention) return
    // The '@' command lives at the input start; replace it with a use-instruction
    // and leave the cursor after it so the user types the task.
    const after = input.slice(mention.start + 1 + mention.query.length)
    const insert = `请使用「${name}」技能完成：`
    onInputChange(insert + after)
    setMention(null)
    setCaret(insert.length)
  }

  function pickAgent(name: string) {
    if (!mention) return
    // The '/' command lives at the input start; replace it with a delegation
    // instruction so the main agent hands the task to this sub-agent (Task tool).
    const after = input.slice(mention.start + 1 + mention.query.length)
    const insert = `请委派「${name}」子代理完成：`
    onInputChange(insert + after)
    setMention(null)
    setCaret(insert.length)
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
  function openSkillPicker() {
    onInputChange('@')
    setMention({ query: '', start: 0, sel: 0, trigger: '@' })
    setCaret(1)
  }
  function openAgentPicker() {
    onInputChange('/')
    setMention({ query: '', start: 0, sel: 0, trigger: '/' })
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

  function handleSend() {
    const text = input.trim()
    if ((!text && attachments.length === 0) || busy) return
    const attach = attachments
    onInputChange('')
    setAttachments([])
    onSend(text, attach)
  }

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
          {mention && mention.trigger === '@' && skillMatches.length > 0 && (
            <div className="absolute left-6 right-6 bottom-full mb-1.5 z-10 bg-surface border border-line2 rounded-[12px] shadow-card overflow-hidden max-h-[300px] overflow-y-auto">
              <div className="px-3.5 pt-2 pb-1 text-[11.5px] text-faint">使用技能 · {skillMatches.length}</div>
              {skillMatches.map((sk, i) => (
                <div
                  key={sk.source + '/' + sk.name}
                  ref={mention.sel === i ? selItemRef : undefined}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    pickSkill(sk.name)
                  }}
                  onMouseEnter={() => setMention((m) => (m ? { ...m, sel: i } : m))}
                  className={`flex items-start gap-2.5 px-3.5 py-2 cursor-pointer ${mention.sel === i ? 'bg-primarysoft' : 'hover:bg-surface2'}`}
                >
                  <span className="text-primaryink flex-none mt-px"><Icon name="book" size={15} /></span>
                  <div className="min-w-0">
                    <div className="text-[13px] text-ink flex items-center gap-1.5">
                      {sk.name}
                      <SourceBadge source={sk.source} />
                    </div>
                    <div className="text-[11.5px] text-faint truncate">{sk.description}</div>
                  </div>
                </div>
              ))}
            </div>
          )}
          {mention && mention.trigger === '/' && agentMatches.length > 0 && (
            <div className="absolute left-6 right-6 bottom-full mb-1.5 z-10 bg-surface border border-line2 rounded-[12px] shadow-card overflow-hidden max-h-[300px] overflow-y-auto">
              <div className="px-3.5 pt-2 pb-1 text-[11.5px] text-faint">委派子代理 · {agentMatches.length}</div>
              {agentMatches.map((ag, i) => (
                <div
                  key={ag.source + '/' + ag.name}
                  ref={mention.sel === i ? selItemRef : undefined}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    pickAgent(ag.name)
                  }}
                  onMouseEnter={() => setMention((m) => (m ? { ...m, sel: i } : m))}
                  className={`flex items-start gap-2.5 px-3.5 py-2 cursor-pointer ${mention.sel === i ? 'bg-primarysoft' : 'hover:bg-surface2'}`}
                >
                  <span className="text-primaryink flex-none mt-px"><Icon name="bot" size={15} /></span>
                  <div className="min-w-0">
                    <div className="text-[13px] text-ink flex items-center gap-1.5">
                      {ag.name}
                      <SourceBadge source={ag.source} />
                    </div>
                    <div className="text-[11.5px] text-faint truncate">{ag.description}</div>
                  </div>
                </div>
              ))}
            </div>
          )}
          {mention && mention.trigger === '#' && (fileMatches.length > 0 || mention.query === '') && (
            <div className="absolute left-6 right-6 bottom-full mb-1.5 z-10 bg-surface border border-line2 rounded-[12px] shadow-card overflow-hidden max-h-[300px] overflow-y-auto">
              <div className="px-3.5 pt-2 pb-1 text-[11.5px] text-faint">引用文件 · {fileMatches.length}{fileMatches.length >= 50 ? '+' : ''}</div>
              {fileMatches.length === 0 && (
                <div className="px-3.5 py-2.5 text-[12.5px] text-faint">该工作区没有可引用的文件</div>
              )}
              {fileMatches.map((p, i) => {
                const slash = p.lastIndexOf('/')
                const name = slash >= 0 ? p.slice(slash + 1) : p
                const dir = slash >= 0 ? p.slice(0, slash) : ''
                return (
                  <div
                    key={p}
                    ref={mention.sel === i ? selItemRef : undefined}
                    onMouseDown={(e) => {
                      e.preventDefault()
                      pickFile(p)
                    }}
                    onMouseEnter={() => setMention((m) => (m ? { ...m, sel: i } : m))}
                    className={`flex items-center gap-2.5 px-3.5 py-1.5 cursor-pointer ${mention.sel === i ? 'bg-primarysoft' : 'hover:bg-surface2'}`}
                  >
                    <span className="w-6 h-6 rounded-[6px] flex-none bg-inset inline-flex items-center justify-center" style={{ color: fileColor(p) }}><Icon name={kindIcon(classifyPreview(p).kind)} size={14} /></span>
                    <span className="text-[13px] text-ink flex-none">{name}</span>
                    {dir && <span className="text-[11.5px] text-faint font-mono truncate min-w-0">{dir}</span>}
                  </div>
                )
              })}
            </div>
          )}
          {attachments.length > 0 && (
            <div className="flex flex-wrap gap-2 mb-1.5">
              {attachments.map((p, i) => {
                const name = basename(p)
                return (
                  <span key={p + i} className="inline-flex items-center gap-1.5 bg-surface2 border border-line2 rounded-[9px] pl-2.5 pr-1 py-1 text-[12px] text-ink max-w-[220px]">
                    <Icon name="file" size={13} />
                    <span className="truncate">{name}</span>
                    <button className="flex-none text-faint hover:text-red px-1 leading-none" title="移除附件" onClick={() => setAttachments((a) => a.filter((x) => x !== p))}>✕</button>
                  </span>
                )
              })}
            </div>
          )}
          <textarea
            ref={taRef}
            className="block w-full resize-none min-h-[46px] max-h-[200px] bg-surface text-ink border border-line2 border-b-0 rounded-t-[14px] px-4 py-3.5 outline-none placeholder:text-faint"
            value={input}
            placeholder="继续对话，@ 技能，/ 子代理，# 文件，按 Enter 发送"
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
              if (mention && mentionCount > 0) {
                if (e.key === 'ArrowDown') {
                  e.preventDefault()
                  setMention((m) => (m ? { ...m, sel: Math.min(m.sel + 1, mentionCount - 1) } : m))
                  return
                }
                if (e.key === 'ArrowUp') {
                  e.preventDefault()
                  setMention((m) => (m ? { ...m, sel: Math.max(m.sel - 1, 0) } : m))
                  return
                }
                if (e.key === 'Enter' || e.key === 'Tab') {
                  e.preventDefault()
                  pickMention(mention.sel)
                  return
                }
                if (e.key === 'Escape') {
                  e.preventDefault()
                  setMention(null)
                  return
                }
              }
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                handleSend()
              }
            }}
          />
          <div
            ref={toolbarRef}
            className="flex items-center justify-between gap-2 bg-surface border border-line2 border-t-0 rounded-b-[14px] px-3 py-[9px] shadow-card"
          >
            {/* Left group never shrinks — labels collapse to icons instead (see compactBar),
                so the buttons keep their shape rather than being squeezed. */}
            <div className="flex items-center gap-1.5 flex-none">
              <div className="relative flex-none">
                <button
                  onClick={() => setAddMenu((v) => !v)}
                  title="添加：技能 / 智能体 / 图片"
                  className="border-none bg-transparent text-muted text-[13px] px-2.5 py-1.5 rounded-lg cursor-pointer inline-flex items-center gap-1.5 hover:bg-surface2 hover:text-ink"
                >
                  <Icon name="plus" size={16} />
                </button>
                <Popover open={addMenu} onClose={() => setAddMenu(false)} placement="up-left" className="w-[180px]">
                  <div onClick={() => { setAddMenu(false); openSkillPicker() }} className="px-3 py-[7px] text-[13px] cursor-pointer text-ink hover:bg-surface2 flex items-center gap-2"><Icon name="book" size={15} /> 技能</div>
                  <div onClick={() => { setAddMenu(false); openAgentPicker() }} className="px-3 py-[7px] text-[13px] cursor-pointer text-ink hover:bg-surface2 flex items-center gap-2"><Icon name="bot" size={15} /> 智能体</div>
                  <div onClick={() => { setAddMenu(false); openFilePicker() }} className="px-3 py-[7px] text-[13px] cursor-pointer text-ink hover:bg-surface2 flex items-center gap-2"><Icon name="hash" size={15} /> 文件</div>
                  <div onClick={() => { setAddMenu(false); pickAttachment() }} className="px-3 py-[7px] text-[13px] cursor-pointer text-ink hover:bg-surface2 flex items-center gap-2"><Icon name="paperclip" size={15} /> 图片附件</div>
                </Popover>
              </div>
              {/* The mode label is the last to go: unlike the toggles below, the shield
                  icon alone carries no hint of which mode is active. */}
              <GhostBtn className="flex-none whitespace-nowrap" onClick={onToggleMode} title={`点击切换权限模式\n当前：${MODE_LABEL[info?.permissionMode ?? ''] ?? '安全模式'}`}>
                <Icon name="shield" size={16} />
                {!tinyBar && (MODE_LABEL[info?.permissionMode ?? ''] ?? '安全模式')}
              </GhostBtn>
              <button
                onClick={onTogglePlan}
                title="计划模式：只调研、产出方案，不做任何修改"
                className={`border text-[13px] px-2.5 py-1.5 rounded-lg cursor-pointer inline-flex items-center gap-1.5 flex-none whitespace-nowrap transition ${info?.planMode ? 'border-primary text-primaryink bg-primarysoft font-medium' : 'border-transparent bg-transparent text-muted hover:bg-surface2 hover:text-ink'}`}
              >
                <Icon name="compass" size={16} />
                {!compactBar && '计划模式'}
              </button>
              {SHOW_THINKING_MODEL && (
              <div className="relative flex-none">
                <button
                  onClick={() => setReasonMenu((v) => !v)}
                  title="思考模型：为本轮注入一套思维方法论"
                  className={`border text-[13px] px-2.5 py-1.5 rounded-lg cursor-pointer inline-flex items-center gap-1.5 flex-none whitespace-nowrap transition ${(info?.reasoningScenario ?? 'off') !== 'off' ? 'border-primary text-primaryink bg-primarysoft font-medium' : 'border-transparent bg-transparent text-muted hover:bg-surface2 hover:text-ink'}`}
                >
                  <Icon name="sparkles" size={16} />
                  {!compactBar && ((info?.reasoningScenario ?? 'off') === 'off' ? '思考模型' : (REASONING_LABEL[info!.reasoningScenario!] ?? info!.reasoningScenario))}
                  <Icon name="chevron-down" size={12} />
                </button>
                <Popover open={reasonMenu} onClose={() => setReasonMenu(false)} placement="up-left" className="w-[224px]">
                  {REASONING.map((r) => (
                    <div
                      key={r.value}
                      onClick={() => chooseReasoning(r.value)}
                      className={`px-3 py-[7px] text-[13px] cursor-pointer ${(info?.reasoningScenario ?? 'off') === r.value ? 'bg-primarysoft text-primaryink font-medium' : 'text-ink hover:bg-surface2'}`}
                    >
                      {r.label}
                    </div>
                  ))}
                </Popover>
              </div>
              )}
              <div className="relative flex-none">
                <button
                  onClick={() => setThinkMenu((v) => !v)}
                  title={`思考强度：让推理模型输出思考过程（reasoning_effort）\n当前：${(info?.thinkingEffort ?? 'off') === 'off' ? '不启用' : (THINKING_LABEL[info!.thinkingEffort!] ?? info!.thinkingEffort)}`}
                  className={`border text-[13px] px-2.5 py-1.5 rounded-lg cursor-pointer inline-flex items-center gap-1.5 flex-none whitespace-nowrap transition ${(info?.thinkingEffort ?? 'off') !== 'off' ? 'border-primary text-primaryink bg-primarysoft font-medium' : 'border-transparent bg-transparent text-muted hover:bg-surface2 hover:text-ink'}`}
                >
                  <Icon name="sparkles" size={16} />
                  {!compactBar && ((info?.thinkingEffort ?? 'off') === 'off' ? '思考强度' : `思考 · ${THINKING_LABEL[info!.thinkingEffort!] ?? info!.thinkingEffort}`)}
                  <Icon name="chevron-down" size={12} />
                </button>
                <Popover open={thinkMenu} onClose={() => setThinkMenu(false)} placement="up-left" className="w-[200px]">
                  {THINKING.map((t) => (
                    <div
                      key={t.value}
                      onClick={() => chooseThinking(t.value)}
                      className={`px-3 py-[7px] text-[13px] cursor-pointer ${(info?.thinkingEffort ?? 'off') === t.value ? 'bg-primarysoft text-primaryink font-medium' : 'text-ink hover:bg-surface2'}`}
                    >
                      {t.label}
                    </div>
                  ))}
                </Popover>
              </div>
            </div>
            {/* Right group takes the remaining pressure: the model name truncates rather
                than pushing the send button out — model ids get long (deepseek-ai/...). */}
            <div className="flex items-center gap-3 min-w-0">
              <div className="relative min-w-0">
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => (modelPickerOpen ? setModelPickerOpen(false) : void openModelPicker())}
                  className={`font-mono text-[12px] text-muted bg-surface2 border border-line px-[11px] py-[5px] rounded-lg inline-flex items-center gap-1.5 min-w-0 ${tinyBar ? 'max-w-[110px]' : compactBar ? 'max-w-[150px]' : 'max-w-[240px]'} hover:border-primary hover:text-ink transition disabled:opacity-50 disabled:cursor-default disabled:hover:border-line disabled:hover:text-muted`}
                  title={busy ? '对话进行中，无法切换模型' : `点击切换模型\n当前：${info?.model ?? ''}`}
                >
                  {!compactBar && <span className="flex-none">模型 ·</span>}
                  <span className="truncate min-w-0">{info?.model}</span>
                  <Icon name="chevron-down" size={12} className="flex-none" />
                </button>
                <ModelPickerPopover
                  open={modelPickerOpen}
                  onClose={() => setModelPickerOpen(false)}
                  placement="up-right"
                  className="w-[320px] max-h-[380px]"
                  options={modelOptions}
                  current={info?.model}
                  limit={10}
                  onPick={(_, o) => { if (o) void onPickModel(o) }}
                />
              </div>
              {busy ? (
                <button className="w-10 h-10 border-none rounded-[11px] flex-none bg-red text-white inline-flex items-center justify-center cursor-pointer shadow-[0_5px_14px_rgba(224,86,74,0.3)] hover:brightness-105" onClick={onStop} title="停止"><Icon name="stop" size={16} /></button>
              ) : (
                <button className="w-10 h-10 border-none rounded-[11px] flex-none bg-primary text-white inline-flex items-center justify-center cursor-pointer shadow-[0_5px_14px_rgba(91,108,240,0.32)] hover:brightness-105 disabled:opacity-40 disabled:shadow-none disabled:cursor-default" onClick={handleSend} disabled={!input.trim() && attachments.length === 0} title="发送"><Icon name="send" size={17} /></button>
              )}
            </div>
          </div>
        </footer>
  )
}
