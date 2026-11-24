// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AssistantImageGenerationNote } from '@/chat/image-generation-note'

const bridge = vi.hoisted(() => ({
  submitQuestion: vi.fn(),
  listeners: new Map<string, (env: { sessionId: string; payload: unknown }) => void>(),
}))

vi.mock('@/core/bridge', () => ({
  submitQuestion: bridge.submitQuestion,
  Events: {
    AssistantDelta: 'assistant:delta',
    AssistantThinking: 'assistant:thinking',
    Retry: 'llm:retry',
    ToolEvent: 'tool:event',
    ContextCompaction: 'context:compaction',
    ContextUsage: 'context:usage',
    TurnEnd: 'turn:end',
    TurnError: 'turn:error',
    PermissionRequest: 'permission:request',
    Warning: 'warning',
    HarmAutoAllow: 'harm:autoallow',
    OABlocked: 'oa:blocked',
    AutomationRunChanged: 'automation:run_changed',
    AutomationContextChanged: 'automation:context_changed',
    AutomationTaskChanged: 'automation:task_changed',
  },
  getAutomationDefaults: vi.fn(async () => null),
  getAutomationConversation: vi.fn(async () => null),
  listSessionAutomationRuns: vi.fn(async () => ({ items: [], nextCursor: '' })),
  onEnvelope: (name: string, cb: (env: { sessionId: string; payload: unknown }) => void) => {
    bridge.listeners.set(name, cb)
    return () => bridge.listeners.delete(name)
  },
  errText: (e: unknown) => e instanceof Error ? e.message : String(e),
  interrupt: vi.fn(),
  listEdits: vi.fn(async () => []),
  releaseArtifact: vi.fn(async () => {}),
  revertEdit: vi.fn(async () => {}),
}))

import { loadImageGenerationNoteMap, noteStorageKey } from '@/chat/image-generation-note'
import { useConversation, type Conversation } from './use-conversation'

const note: AssistantImageGenerationNote = { modelLabel: 'image-model', sizeLabel: '1024x1024' }
let root: Root
let host: HTMLDivElement
let conversation: Conversation

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  localStorage.clear()
  bridge.listeners.clear()
  bridge.submitQuestion.mockReset()
  host = document.createElement('div')
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.restoreAllMocks()
})

function Harness({ generationNote = note }: { generationNote?: AssistantImageGenerationNote | null }) {
  const infoRef = { current: { sessionId: 'S' } as never }
  conversation = useConversation({
    focusedId: 'S',
    infoRef,
    permissions: { pending: null, enqueue: vi.fn(), clear: vi.fn() },
    onFilesChanged: vi.fn(),
    onOpenPreview: vi.fn(),
    showToast: vi.fn(),
    getImageGenerationNote: async () => generationNote,
  })
  return null
}

async function render(generationNote: AssistantImageGenerationNote | null = note) {
  await act(async () => root.render(<Harness generationNote={generationNote} />))
}

async function emit(name: string, payload: unknown, sessionId = 'S') {
  await act(async () => bridge.listeners.get(name)?.({ sessionId, payload }))
  await act(async () => { await Promise.resolve() })
}

const turnEnd = {
  text: 'done', stopReason: 'stop', iterations: 1, toolResultCount: 0,
  inputTokens: 0, outputTokens: 0, contextTokens: 0, stopped: false,
}

describe('image generation assistant note timing', () => {
  it('does not persist or render configured settings when the turn has no image_generate', async () => {
    bridge.submitQuestion.mockResolvedValue({ questionId: 'q-no-image' })
    await render()
    await act(async () => { await conversation.send('draw a diagram') })
    await emit('assistant:delta', { text: '```mermaid\ngraph TD\nA-->B\n```' })
    await emit('turn:end', turnEnd)

    expect(loadImageGenerationNoteMap('S')).toEqual({})
    expect(conversation.blocks.some((b) => b.kind === 'assistant' && b.imageGenerationNote)).toBe(false)
  })

  it('stamps and persists once after image_generate and questionId are both known', async () => {
    bridge.submitQuestion.mockResolvedValue({ questionId: 'q-image' })
    await render()
    await act(async () => { await conversation.send('draw an image') })
    await emit('assistant:delta', { text: '图片已生成' })
    await emit('tool:event', { type: 'completed', toolName: 'image_generate', toolUseID: 'tu-image' })
    await emit('turn:end', turnEnd)

    expect(loadImageGenerationNoteMap('S')).toEqual({ 'q-image': note })
    expect(conversation.blocks.filter((b) => b.kind === 'assistant' && b.imageGenerationNote)).toHaveLength(1)
  })

  it('stamps unconfigured note when model skips image_generate but guides to settings', async () => {
    const unconfigured: AssistantImageGenerationNote = { modelLabel: '未配置', sizeLabel: '未配置' }
    bridge.submitQuestion.mockResolvedValue({ questionId: 'q-unconfigured' })
    await render(unconfigured)
    await act(async () => { await conversation.send('生成一件T恤') })
    await emit('assistant:delta', {
      text: '目前暂时无法为您生成图片。请点击回复末尾的「修改生图设置」。',
    })
    await emit('turn:end', turnEnd)

    expect(loadImageGenerationNoteMap('S')).toEqual({ 'q-unconfigured': unconfigured })
    const stamped = conversation.blocks.filter((b) => b.kind === 'assistant' && b.imageGenerationNote)
    expect(stamped).toHaveLength(1)
    expect(stamped[0]).toMatchObject({ imageGenerationNote: unconfigured })
  })

  it('waits for a late questionId after image_generate and TurnEnd already arrived', async () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    let resolveReceipt!: (value: { questionId: string }) => void
    bridge.submitQuestion.mockReturnValueOnce(new Promise((resolve) => { resolveReceipt = resolve }))
    await render()
    let sending!: Promise<boolean>
    await act(async () => {
      sending = conversation.send('draw an image')
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(bridge.submitQuestion).toHaveBeenCalledTimes(1)
    await emit('assistant:delta', { text: '图片已生成' })
    await emit('tool:event', { type: 'completed', toolName: 'image_generate', toolUseID: 'tu-race' })
    await emit('turn:end', turnEnd)
    expect(loadImageGenerationNoteMap('S')).toEqual({})

    await act(async () => {
      resolveReceipt({ questionId: 'q-late' })
      await sending
    })
    expect(loadImageGenerationNoteMap('S')).toEqual({ 'q-late': note })
    expect(setItem.mock.calls.filter(([key]) => key === noteStorageKey('S'))).toHaveLength(1)
    expect(conversation.blocks.filter((b) => b.kind === 'assistant' && b.imageGenerationNote)).toHaveLength(1)
  })
})
