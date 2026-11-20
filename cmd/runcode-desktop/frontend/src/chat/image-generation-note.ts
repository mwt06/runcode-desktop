// 助手回复尾注：冻结的生图模型与尺寸策略。纯函数 + localStorage 映射，不依赖 React。
import type { Block } from './blocks'

export type AssistantImageGenerationNote = {
  modelLabel: string
  sizeLabel: string
}

export const UNFIXED_SIZE_LABEL = '不固定尺寸'

/** 展示用尺寸文案：去掉旧版出站兜底说明，避免脚注过长。 */
export function displayImageGenerationSizeLabel(label: string): string {
  const cleaned = label.replace(/（未指定时出站补最小正方形）/g, '').trim()
  return cleaned || UNFIXED_SIZE_LABEL
}

/** 去掉模型常见套话；客户端已在卡片下方展示预览，不必再提桌面预览面板。 */
export function scrubImageGenerationAssistantText(text: string): string {
  return text
    .replace(/，已在桌面预览中打开。/g, '。')
    .replace(/已在桌面预览中打开。/g, '')
    .replace(/，已在桌面预览中打开/g, '')
    .replace(/已在桌面预览中打开/g, '')
}

export function formatImageGenerationNote(input: { modelName?: string | null; size?: string | null }): AssistantImageGenerationNote {
  const model = (input.modelName ?? '').trim()
  const size = (input.size ?? '').trim()
  // 未选模型时模型与尺寸都显示「未配置」，与失败尾注／设置入口一致；有模型但未固定尺寸仍用「不固定尺寸」。
  if (!model) {
    return { modelLabel: '未配置', sizeLabel: '未配置' }
  }
  return {
    modelLabel: model,
    sizeLabel: size || UNFIXED_SIZE_LABEL,
  }
}

/** 冻结尾注是否表示尚未选择生图模型（含旧版「未设置」文案）。 */
export function isUnconfiguredImageGenerationNote(note: AssistantImageGenerationNote | null | undefined): boolean {
  const label = (note?.modelLabel ?? '').trim()
  return label === '未配置' || label === '未设置'
}

/**
 * 未配置且本回合未实际发出 image_generate 时，是否仍应盖尾注。
 * 工具说明要求「请勿调用」，模型常只写失败说明；若仍要求必须 observe，
 * 末尾就不会出现「修改生图设置」。本判定只认未配置 + 本回合出图意图／失败引导。
 */
export function shouldStampUnconfiguredImageGenerationNote(blocks: Block[]): boolean {
  const lastUser = blocks.map((b) => b.kind).lastIndexOf('user')
  if (lastUser < 0) return false
  const user = blocks[lastUser]
  const userText = user?.kind === 'user' ? user.text : ''
  const asstText = blocks
    .slice(lastUser + 1)
    .filter((b): b is Extract<Block, { kind: 'assistant' }> => b.kind === 'assistant')
    .map((b) => b.text)
    .join('\n')
  // 助手已在引导去改设置／说明无法出图
  if (/修改生图设置|图像生成模型|生图模型|无法[^\n]{0,24}生成图片|没有选择[^\n]{0,16}(生图|图像生成)/.test(asstText)) {
    return true
  }
  // 用户本意就是位图／海报／实物效果等出图；避免「画一个流程图」这类逻辑图误盖
  if (
    /文生图|画一张|来一张|出一张/.test(userText)
    || (/生成/.test(userText) && /(照片|海报|插画|图片|T恤|衣服|壁纸|一张图)/.test(userText))
    || /生成\S{0,8}图/.test(userText)
  ) {
    return true
  }
  return false
}

export function noteStorageKey(sessionId: string): string {
  return `imagegen.note.${sessionId}`
}

export type ImageGenerationNoteMap = Record<string, AssistantImageGenerationNote>

export function loadImageGenerationNoteMap(sessionId: string): ImageGenerationNoteMap {
  if (!sessionId) return {}
  try {
    const raw = localStorage.getItem(noteStorageKey(sessionId))
    if (!raw) return {}
    const parsed = JSON.parse(raw) as ImageGenerationNoteMap
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

export function saveImageGenerationNote(sessionId: string, questionId: string, note: AssistantImageGenerationNote): void {
  if (!sessionId || !questionId) return
  const map = loadImageGenerationNoteMap(sessionId)
  map[questionId] = note
  localStorage.setItem(noteStorageKey(sessionId), JSON.stringify(map))
}

// stampTurnAssistantNote 把本回合冻结的尾注写到最后一个已完成的助手气泡上。
export function stampTurnAssistantNote(blocks: Block[], note: AssistantImageGenerationNote): Block[] {
  let lastAsst = -1
  const lastUser = blocks.map((b) => b.kind).lastIndexOf('user')
  for (let i = blocks.length - 1; i > lastUser; i--) {
    const b = blocks[i]
    if (b.kind === 'assistant' && !b.streaming) {
      lastAsst = i
      break
    }
  }
  if (lastAsst < 0) return blocks
  const target = blocks[lastAsst]
  if (target.kind !== 'assistant') return blocks
  if (target.imageGenerationNote?.modelLabel === note.modelLabel && target.imageGenerationNote?.sizeLabel === note.sizeLabel) {
    return blocks
  }
  return blocks.map((b, i) => (i === lastAsst && b.kind === 'assistant' ? { ...b, imageGenerationNote: note } : b))
}

// rehydrateAssistantNotes 按 user.questionId 把持久化尾注贴回恢复后的助手块；
// 每个提问回合只贴在最后一个已完成的助手气泡上（与 stampTurnAssistantNote 一致）。
export function rehydrateAssistantNotes(blocks: Block[], map: ImageGenerationNoteMap): Block[] {
  if (!map || Object.keys(map).length === 0) return blocks
  const stampAt = new Set<number>()
  let turnStart = -1
  let turnNote: AssistantImageGenerationNote | undefined
  const flush = (endExclusive: number) => {
    if (!turnNote) return
    for (let i = endExclusive - 1; i > turnStart; i--) {
      const b = blocks[i]
      if (b.kind === 'assistant' && !b.streaming) {
        stampAt.add(i)
        return
      }
    }
  }
  for (let i = 0; i < blocks.length; i++) {
    const b = blocks[i]
    if (b.kind === 'user') {
      flush(i)
      turnStart = i
      turnNote = b.questionId ? map[b.questionId] : undefined
    }
  }
  flush(blocks.length)
  if (stampAt.size === 0) return blocks
  let changed = false
  const next = blocks.map((b, i) => {
    if (!stampAt.has(i) || b.kind !== 'assistant' || b.imageGenerationNote) return b
    const note = (() => {
      for (let j = i; j >= 0; j--) {
        const u = blocks[j]
        if (u.kind === 'user' && u.questionId && map[u.questionId]) return map[u.questionId]
      }
      return undefined
    })()
    if (!note) return b
    changed = true
    return { ...b, imageGenerationNote: note }
  })
  return changed ? next : blocks
}

export function questionIdForUserBlock(blocks: Block[], userBlockId: string): string | undefined {
  const b = blocks.find((x) => x.id === userBlockId && x.kind === 'user')
  return b && b.kind === 'user' ? b.questionId : undefined
}
