import type { QuestionDraft } from '@/core/bridge'
import { GhostBtn } from '@/ui/ghost-btn'
// composer 是对话输入区：多行输入框、@技能 / /子代理 / #文件 三种触发的选择器、
// 附件（选图、粘贴），以及底部工具条(拆到 ./toolbar)。选择器与附件等 UI 状态全部
// 内化；会话状态的变更（切模式/切模型/发送/停止）一律经 on* 回调交还 App——本组件
// 从不直接改会话状态。
import { useCallback, useEffect, useId, useRef, useState, type RefObject, type Dispatch, type SetStateAction } from 'react'
import { Icon } from '@/ui/icons'
import { basename } from '@/core/paths'
import { errText, pickImageAttachment, savePastedFile, listAgents, listSkills, type AgentInfo, type SessionInfo, type SkillInfo } from '@/core/bridge'
import { classifyPreview, kindIcon } from '@/preview/classify'
import { type ModelOption } from '@/ui/model-picker'
import { isImage, pastedName, sendText, shouldIntakeDrop, shouldIntakePaste, tooLargeMessage, type Attachment } from './paste'
import { composerKeyAction } from './keymap'
import { applyMention, computeMention, matchByNameOrDesc, rankFileMatches, type MentionTrigger } from './mention'
import { AgentPicker, FilePicker, SkillPicker } from './mention-picker'
import { ComposerToolbar } from './toolbar'
import { useComposerResize } from './use-composer-resize'
import { usePickerDismiss } from './use-picker-dismiss'
import { ComposerMascot } from './mascot'
import { ScenarioBar, ScenarioPanel, type BuiltinAction } from './scenario-bar'
import { visibleScenarios, type Scenario } from '@/core/scenarios'
import { scenariosForProfile } from '@/core/scenario-profiles'
import { BRAND } from '@/core/brand'

// Composer 的对外契约：input 受控于 App（插件页“使用技能/委派子代理”要往输入框
// 追加文案并聚焦，故 input 与 taRef 由 App 持有）；files 归 App（文件浏览器/回复
// 产物条也要用），选择器打开时经 onRefreshFiles 请求刷新。
export function Composer({
  attachments, setAttachments, retry, retryDisabled, submitting, onCancelRetry, onRemoveOriginal,
  input,
  onInputChange,
  taRef,
  chatScrollRef,
  busy,
  toast,
  info,
  files,
  sessionId,
  onRefreshFiles,
  onSend,
  onNotify,
  onStop,
  onToggleMode,
  onTogglePlan,
  onChooseReasoning,
  onChooseThinking,
  onPickModel,
  builtinScenarios = {},
  onPickScenario,
}: {
  attachments: Attachment[]
  setAttachments: Dispatch<SetStateAction<Attachment[]>>
  retry?: { original: QuestionDraft; images: number[] }
  retryDisabled: boolean
  submitting: boolean
  onCancelRetry: () => void
  onRemoveOriginal: (index: number) => void
  input: string
  onInputChange: (v: string) => void
  taRef: RefObject<HTMLTextAreaElement>
  chatScrollRef: RefObject<HTMLDivElement>
  busy: boolean
  toast: string
  info: SessionInfo | null
  files: string[]
  sessionId?: string
  onRefreshFiles: () => void
  // onSend 的三个参数：发给模型的正文、图片附件路径、以及可选的“气泡里显示成什么”
  // （非图片附件的路径写在正文里，用户自己那句话才是气泡该显示的东西）。
  onSend: (text: string, attachments: string[], display?: string) => Promise<boolean>
  // onNotify 是本组件唯一的报错出口（粘贴超限、落盘失败），由 App 接到 toast 上。
  onNotify?: (msg: string) => void
  onStop: () => void
  onToggleMode: () => void
  onTogglePlan: () => void
  onChooseReasoning: (scenario: string) => void
  onChooseThinking: (effort: string) => void
  onPickModel: (choice: ModelOption) => Promise<void> | void
  // builtinScenarios 是「点了直接执行」的分类动作（目前只有录音纪要），按
  // core/scenarios 的 BUILTIN_CATEGORY 取值。
  builtinScenarios?: Record<string, BuiltinAction>
  // onPickScenario 把选中的场景交回 App：填提示词、选中占位符都要动 taRef 与
  // input，那两样都归 App 持有。
  onPickScenario: (s: Scenario) => void
}) {
  const inputId = useId()
  const resize = useComposerResize(taRef, chatScrollRef)
  const [mention, setMention] = useState<{ query: string; start: number; sel: number; trigger: MentionTrigger } | null>(null)
  const closeMention = useCallback(() => setMention(null), [])
  const [sending, setSending] = useState(false)
  const sendingRef = useRef(false)
  const originalImages = (retry?.original.images ?? []).filter((i) => retry?.images.includes(i.index))
  const locked = submitting || sending || (!!retry && retryDisabled)
  // dropping = 正有文件悬在输入区上方，用来显示"松开以添加附件"。
  const [dropping, setDropping] = useState(false)
  // 展开着的场景分类 id；'' = 没展开。与 mention 一样是纯界面状态，留在本组件。
  const [scenarioCat, setScenarioCat] = useState('')
  const closeScenario = useCallback(() => setScenarioCat(''), [])
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

  const mentionVisible = !!mention && (mentionCount > 0 || (mention.trigger === '#' && mention.query === ''))
  const mentionRegion = usePickerDismiss(mentionVisible && mention ? `${mention.trigger}:${mention.start}` : '', closeMention)

  function syncMention(value: string, cursor: number) {
    mentionRegion.keepOpen()
    const m = computeMention(value, cursor)
    if (m) closeScenario()
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
    mentionRegion.keepOpen()
    closeScenario()
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
    mentionRegion.keepOpen()
    closeScenario()
    onInputChange(trigger)
    setMention({ query: '', start: 0, sel: 0, trigger })
    setCaret(1)
  }
  // Pick an image from disk and attach it to the next message. The path is kept in
  // `attachments` and the bytes are read backend-side at send time.
  async function pickAttachment() {
    try {
      const p = await pickImageAttachment()
      if (p) addAttachment({ path: p, image: true })
    } catch {
      /* cancelled or unavailable */
    }
  }

  function addAttachment(next: Attachment) {
    setAttachments((a) => (a.some((x) => x.path === next.path) ? a : [...a, next]))
  }

  // intake 是粘贴与拖放共用的收取：把浏览器给的 File 交后端落盘，落成附件。
  //
  // WebView 里这两种途径拿到的都只有字节，没有路径——剪贴板里的截图本来就不是文件，
  // 而从资源管理器来的文件，浏览器也不把真实路径交给页面。所以先交后端落盘
  // （savePastedFile），拿回来的路径与"选一张图"得到的完全等价。
  //
  // 串行处理：并发几个几 MB 的 base64 只会一起挤 WebView 那一条 IPC。
  async function intake(files: File[]) {
    for (const f of files) {
      const tooLarge = tooLargeMessage(f)
      if (tooLarge) {
        onNotify?.(tooLarge)
        continue
      }
      try {
        const path = await savePastedFile(f, pastedName(f))
        addAttachment({ path, image: isImage(f) })
      } catch (err) {
        onNotify?.(errText(err))
      }
    }
  }

  // 粘贴。收不收由 shouldIntakePaste 定（有文本就让路，理由在那儿）。
  async function handlePaste(e: React.ClipboardEvent<HTMLTextAreaElement>) {
    const data = e.clipboardData
    const files = Array.from(data?.files ?? [])
    if (!shouldIntakePaste(files.length, data?.getData('text/plain') ?? '')) return
    e.preventDefault()
    await intake(files)
  }

  // 拖放。dragover 必须 preventDefault，否则浏览器根本不会派发 drop——这是 HTML5
  // 拖放最容易漏的一步，漏了的表现是"拖进去没反应"，而且没有任何报错。
  //
  // Windows 上还有一道前置条件在 Go 那边：主窗要开 EnableFileDrop，否则 Wails 的
  // runtime 会给外部文件拖拽显示"禁止放置"光标并吞掉 drop（见 main.go 那处注释）。
  function handleDragOver(e: React.DragEvent<HTMLElement>) {
    if (!e.dataTransfer?.types.includes('Files')) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'copy'
    setDropping(true)
  }

  // 子元素之间移动也会冒出 dragleave（每跨一个孩子一次），只有真正离开整块输入区
  // 才收起高亮；否则拖着文件在输入框上晃一下，提示就闪个不停。
  function handleDragLeave(e: React.DragEvent<HTMLElement>) {
    if (e.currentTarget.contains(e.relatedTarget as Node | null)) return
    setDropping(false)
  }

  async function handleDrop(e: React.DragEvent<HTMLElement>) {
    setDropping(false)
    const files = Array.from(e.dataTransfer?.files ?? [])
    if (!shouldIntakeDrop(e.dataTransfer?.types, files.length)) return
    e.preventDefault()
    await intake(files)
  }

  // 忙时不再拦截:onSend 会把消息排进补充队列(回合结束后自动发)。所以这里发/排队
  // 走同一条路,由下游按 busy 决定,组件不必自己分叉。
  //
  // 两类附件在这里分道：图片进多模态请求（模型直接看图），其它文件只能把路径写进
  // 正文，让模型自己去 Read/ReadOffice。
  //
  // display 让气泡显示用户自己那句话，而不是拼上附件路径的完整正文。一个字没打、
  // 只粘了文件时传 undefined，气泡退回显示正文——否则那条气泡会是空的。
  async function handleSend() {
    if (locked || sendingRef.current) return
    const text = input.trim()
    if (!text && attachments.length === 0 && originalImages.length === 0) return
    const images = attachments.filter((a) => a.image).map((a) => a.path)
    const body = sendText(input, attachments)
    sendingRef.current = true
    setSending(true)
    try { await onSend(body, images, text || undefined) }
    catch (e) { onNotify?.(errText(e)) }
    finally { sendingRef.current = false; setSending(false) }
  }

  const hoverMention = (i: number) => setMention((m) => (m ? { ...m, sel: i } : m))
  // 本次构建实际画得出来的分类：内置功能分类要有 App 交来的动作才算有（见
  // visibleScenarios），否则整类不画。
  const categories = visibleScenarios(scenariosForProfile(BRAND.scenarioProfile), builtinScenarios)
  const scenarioOpen = categories.find((c) => c.id === scenarioCat) ?? null
  const scenarioRegion = usePickerDismiss(!mention && scenarioOpen ? scenarioOpen.id : '', closeScenario)

  return (
    // data-file-drop-target 是给 Wails 的 runtime 看的：它在 documentElement 上统一
    // 接管外部文件拖拽，只在带这个属性的元素上放行（其余地方显示"禁止放置"光标）。
    // 属性缺了的话，拖到输入框上会是一个禁止图标，而 drop 永远不来。
    <footer
      ref={resize.footerRef}
      {...mentionRegion.pointerProps}
      data-file-drop-target
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={(e) => void handleDrop(e)}
      className="flex-none relative px-6 pt-3.5 pb-[18px] w-full max-w-[1200px] mx-auto"
    >
      {/* 拖放提示。pointer-events-none 是关键：这层若吃掉指针事件，dragleave 会在
          它一出现就立刻触发，提示随即消失，drop 也落不到 footer 上。 */}
      {dropping && (
        <div className="absolute inset-2 z-20 pointer-events-none flex items-center justify-center gap-2 rounded-card border-2 border-dashed border-primary bg-primarysoft/95">
          <span className="text-primaryink"><Icon name="paperclip" size={16} /></span>
          <span className="text-[13px] text-primaryink">松开以添加附件</span>
        </div>
      )}
      {toast && (
        <div className="absolute left-0 right-0 -top-1 flex justify-center pointer-events-none z-10">
          <div className="anim-rise px-3 py-1.5 rounded-full text-[12px] bg-surface2 border border-line2 text-muted shadow-card">
            {toast}
          </div>
        </div>
      )}
      {/* contents 不建立新的定位参照；点击边界只含候选，鼠标活动范围是整个 footer。 */}
      <div className="contents" ref={mentionRegion.regionRef}>
        {mention?.trigger === '@' && skillMatches.length > 0 && (
          <SkillPicker items={skillMatches} sel={mention.sel} selRef={selItemRef} onHover={hoverMention} onPick={pick} />
        )}
        {mention?.trigger === '/' && agentMatches.length > 0 && (
          <AgentPicker items={agentMatches} sel={mention.sel} selRef={selItemRef} onHover={hoverMention} onPick={pick} />
        )}
        {mention?.trigger === '#' && (fileMatches.length > 0 || mention.query === '') && (
          <FilePicker items={fileMatches} sel={mention.sel} selRef={selItemRef} onHover={hoverMention} onPick={pick} />
        )}
      </div>
      <div className="flow-root" ref={scenarioRegion.regionRef} {...scenarioRegion.pointerProps}>
        {info?.planMode && (
          <div className="flex items-center gap-2.5 mb-2 bg-primarysoft border border-primary rounded-xl px-3.5 py-2.5">
            <span className="text-primaryink flex-none"><Icon name="compass" size={16} /></span>
            <span className="text-[13px] text-primaryink flex-1 min-w-0">计划模式：先调研、产出方案；可以新建和修改文件，但不会删除文件或执行命令。方案给出后可选择如何继续。</span>
          </div>
        )}
        {!mention && scenarioOpen && (
          <ScenarioPanel category={scenarioOpen} onPick={(s) => { closeScenario(); onPickScenario(s) }} />
        )}
        <ScenarioBar
          categories={categories}
          openId={!mention ? scenarioCat : ''}
          onToggle={(id) => { closeMention(); setScenarioCat((cur) => (cur === id ? '' : id)) }}
          onClose={closeScenario}
          builtins={builtinScenarios}
        />
      </div>
      {/* 这层 relative 是插画的定位参照：它绝对定位贴在输入框上沿(bottom-full)，
          **不占布局高度**——放进正常流里它会自己占一行，把上面「录音纪要」那排
          快捷技能整体顶走。它的下半身本来就被画布裁掉，是「从边缘探头」的画法，
          中间留空隙就会显得是渲染缺了一块。 */}
      <div className="relative">
      <ComposerMascot />
      {retry && <div className="mb-2 bg-surface2 border border-line2 rounded-card px-3 py-2 text-[12px] text-muted">
        <div className="flex items-center justify-between gap-2"><span className="font-medium text-ink2">正在修改原提问</span><GhostBtn disabled={submitting || sending} onClick={onCancelRetry}>取消修改</GhostBtn></div>
        <p>将从这条提问重新开始。原记录保留，已生成的文件不会撤销。</p>
        {originalImages.length > 0 && <div className="flex flex-wrap gap-1.5 mt-2">{originalImages.map((i) => <span key={i.index} className="inline-flex items-center gap-1 bg-surface rounded-field px-2 py-1"><Icon name="file" size={12} />{i.name}<GhostBtn disabled={locked} title={`移除${i.name}`} onClick={() => onRemoveOriginal(i.index)}>✕</GhostBtn></span>)}</div>}
      </div>}
      <div className="relative">
      <div
        role="separator"
        aria-label="调整输入框高度"
        aria-orientation="horizontal"
        aria-controls={inputId}
        aria-valuemin={resize.bounds.min}
        aria-valuemax={resize.bounds.max}
        aria-valuenow={resize.height}
        aria-valuetext={`${resize.height} 像素`}
        tabIndex={0}
        title="拖动调整输入框高度，双击恢复默认"
        className="group/resize absolute -top-1.5 left-1/2 -translate-x-1/2 z-10 flex h-3 w-16 items-center justify-center cursor-row-resize touch-none select-none rounded-full outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
        {...resize.handleProps}
      >
        <span className="h-1 w-8 rounded-full bg-line2 transition-colors group-hover/resize:bg-faint group-focus-visible/resize:bg-primary group-active/resize:bg-primary" />
      </div>
      {/* 附件条是输入卡片的顶层：卡片本身由「附件条 + 输入框 + 工具条」三块拼成，
          各自只画自己那一边的边框，靠 border-t-0/border-b-0 消掉接缝处的双线。
          放在卡片里而不是卡片外，是因为附件属于「这条待发的消息」，跟着输入内容走；
          摆在快捷技能栏上面会让人以为它是另一块独立区域。 */}
      {attachments.length > 0 && (
        <div className="flex flex-wrap gap-1.5 bg-surface border-x border-t border-line2 rounded-t-card px-3 pt-2.5 pb-1">
          {attachments.map((a) => (
            <span key={a.path} title={a.path} className="inline-flex items-center gap-1.5 bg-surface2 border border-line2 rounded-field pl-2.5 pr-1 py-1 text-[12px] text-ink max-w-[220px]">
              {/* 图标按类型走，与预览页同一套映射，芯片和预览标签页认得出是同一个文件。 */}
              <Icon name={kindIcon(classifyPreview(a.path).kind)} size={13} />
              <span className="truncate">{basename(a.path)}</span>
              <button className="flex-none text-faint hover:text-red px-1 leading-none" title="移除附件" onClick={() => setAttachments((list) => list.filter((x) => x.path !== a.path))}>✕</button>
            </span>
          ))}
        </div>
      )}
      <textarea
        ref={taRef}
        id={inputId}
        style={{ height: resize.height }}
        readOnly={submitting || sending}
        className={`block w-full resize-none overflow-y-auto bg-surface text-ink border-x border-line2 px-4 py-3.5 outline-none placeholder:text-faint ${attachments.length > 0 ? '' : 'border-t rounded-t-card'}`}
        value={input}
        placeholder={busy ? '回合进行中——输入后 Enter 立即补充，模型会在当前步骤结束后看到' : '继续对话，@ 技能，/ 子代理，# 文件；Enter 发送，Shift+Enter 换行'}
        onChange={(e) => {
          onInputChange(e.target.value)
          syncMention(e.target.value, e.target.selectionStart ?? e.target.value.length)
        }}
        // 点回输入框只关闭候选；继续键入/移动光标才重新匹配，避免同一次点击又弹出。
        onClick={closeMention}
        onPaste={(e) => void handlePaste(e)}
        onKeyUp={(e) => {
          if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) {
            syncMention(input, e.currentTarget.selectionStart ?? input.length)
          }
        }}
        onKeyDown={(e) => {
          mentionRegion.keepOpen()
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
              void handleSend()
              break
          }
        }}
      />
      </div>
      </div>
      <ComposerToolbar
        info={info}
        busy={busy}
        canSend={!locked && (!!input.trim() || attachments.length > 0 || originalImages.length > 0)}
        sendLabel={retry ? '重新生成' : undefined}
        onOpenSkillPicker={() => openTriggerPicker('@')}
        onOpenAgentPicker={() => openTriggerPicker('/')}
        onOpenFilePicker={openFilePicker}
        onPickAttachment={() => void pickAttachment()}
        onToggleMode={onToggleMode}
        onTogglePlan={onTogglePlan}
        onChooseReasoning={onChooseReasoning}
        onChooseThinking={onChooseThinking}
        onPickModel={onPickModel}
        onSend={() => void handleSend()}
        onStop={onStop}
      />
    </footer>
  )
}
