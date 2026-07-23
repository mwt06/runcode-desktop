// 输入框底部工具条：+ 菜单、权限模式、计划模式、思考强度、模型选择、发送/停止。
// 自行测量自身宽度做响应式(见下方注释)，会话状态一律经 on* 回调交还上层。
import { useEffect, useRef, useState } from 'react'
import { Icon } from '@/ui/icons'
import { GhostBtn } from '@/ui/ghost-btn'
import { Popover } from '@/ui/popover'
import { ModelPickerPopover, type ModelOption } from '@/ui/model-picker'
import { customModelOptionSub } from '@/core/custom-models'
import { listCustomModels, sessionModels, type SessionInfo } from '@/core/bridge'

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

export function ComposerToolbar({
  info,
  busy,
  canSend,
  onOpenSkillPicker,
  onOpenAgentPicker,
  onOpenFilePicker,
  onPickAttachment,
  onToggleMode,
  onTogglePlan,
  onChooseReasoning,
  onChooseThinking,
  onPickModel,
  onSend,
  onStop,
}: {
  info: SessionInfo | null
  busy: boolean
  canSend: boolean
  onOpenSkillPicker: () => void
  onOpenAgentPicker: () => void
  onOpenFilePicker: () => void
  onPickAttachment: () => void
  onToggleMode: () => void
  onTogglePlan: () => void
  onChooseReasoning: (scenario: string) => void
  onChooseThinking: (effort: string) => void
  onPickModel: (choice: ModelOption) => Promise<void> | void
  onSend: () => void
  onStop: () => void
}) {
  const [addMenu, setAddMenu] = useState(false)
  const [reasonMenu, setReasonMenu] = useState(false)
  const [thinkMenu, setThinkMenu] = useState(false)
  // 对话内模型选择器：点底部模型名弹出，模糊检索，最多显示 10 个。平台(通行证)模型
  // 与自定义直连模型合并展示，都能被搜索和切换；切换本身（换连接/重建会话）由
  // 上层的 onPickModel 完成。
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
  // 菜单先收起再回调上层——与拆分前 chooseReasoning/chooseThinking 的时序一致。
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

  return (
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
            <div onClick={() => { setAddMenu(false); onOpenSkillPicker() }} className="px-3 py-[7px] text-[13px] cursor-pointer text-ink hover:bg-surface2 flex items-center gap-2"><Icon name="book" size={15} /> 技能</div>
            <div onClick={() => { setAddMenu(false); onOpenAgentPicker() }} className="px-3 py-[7px] text-[13px] cursor-pointer text-ink hover:bg-surface2 flex items-center gap-2"><Icon name="bot" size={15} /> 智能体</div>
            <div onClick={() => { setAddMenu(false); onOpenFilePicker() }} className="px-3 py-[7px] text-[13px] cursor-pointer text-ink hover:bg-surface2 flex items-center gap-2"><Icon name="hash" size={15} /> 文件</div>
            <div onClick={() => { setAddMenu(false); onPickAttachment() }} className="px-3 py-[7px] text-[13px] cursor-pointer text-ink hover:bg-surface2 flex items-center gap-2"><Icon name="paperclip" size={15} /> 图片附件</div>
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
          <button className="w-10 h-10 border-none rounded-[11px] flex-none bg-primary text-white inline-flex items-center justify-center cursor-pointer shadow-[0_5px_14px_rgba(91,108,240,0.32)] hover:brightness-105 disabled:opacity-40 disabled:shadow-none disabled:cursor-default" onClick={onSend} disabled={!canSend} title="发送"><Icon name="send" size={17} /></button>
        )}
      </div>
    </div>
  )
}
