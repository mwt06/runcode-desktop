// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  saveCustomModel: vi.fn(),
  deleteCustomModel: vi.fn(),
}))

vi.mock('@/core/bridge', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/core/bridge')>()
  return {
    ...actual,
    ...api,
    errText: (error: unknown) => error instanceof Error ? error.message : String(error),
  }
})

vi.mock('./codex-login', () => ({ CodexLoginRow: () => null }))

import type { CustomModel } from '@/core/bridge'
import { CustomModelsSection } from './custom-models'
import { SettingsSaveProvider } from './settings-save-provider'

const MODEL: CustomModel = {
  name: 'local',
  provider: 'openai-compatible',
  model: 'qwen',
  baseURL: 'http://127.0.0.1:11434',
  hasAPIKey: false,
}

let host: HTMLDivElement
let root: Root
let changed: (list: CustomModel[]) => void

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  api.saveCustomModel.mockReset()
  api.deleteCustomModel.mockReset()
  api.saveCustomModel.mockImplementation(async () => [MODEL])
  changed = vi.fn()
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

async function mount() {
  await act(async () => root.render(
    <SettingsSaveProvider>
      <CustomModelsSection models={[MODEL]} onChanged={changed} />
    </SettingsSaveProvider>,
  ))
}

async function edit() {
  const button = Array.from(host.querySelectorAll('button')).find((node) => node.textContent === '编辑')
  if (!button) throw new Error('edit button missing')
  await act(async () => button.click())
}

function form() {
  return host.querySelector<HTMLElement>('.custom-model-form')!
}

function nameInput() {
  return form().querySelector<HTMLInputElement>('input[placeholder="显示名称（如 本地 Ollama）"]')!
}

async function typeName(value: string) {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    setter.call(nameInput(), value)
    nameInput().dispatchEvent(new InputEvent('input', { bubbles: true, data: value, inputType: 'insertText' }))
  })
}

describe('CustomModelsSection autosave', () => {
  it('removes the manual save button for an existing model', async () => {
    await mount()
    await edit()
    expect(Array.from(host.querySelectorAll('button')).some((button) => button.textContent === '保存修改')).toBe(false)
    expect(host.textContent).toContain('收起编辑')
  })

  it('does not save while focus moves inside the form', async () => {
    await mount()
    await edit()
    await typeName('local-renamed')
    await act(async () => {
      nameInput().focus()
      form().querySelector<HTMLSelectElement>('select')!.focus()
    })
    expect(api.saveCustomModel).not.toHaveBeenCalled()
  })

  it('saves once when focus leaves the whole form', async () => {
    await mount()
    await edit()
    await typeName('local-renamed')
    await act(async () => {
      nameInput().focus()
      nameInput().blur()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(api.saveCustomModel).toHaveBeenCalledTimes(1)
    expect(api.saveCustomModel.mock.calls[0][0]).toMatchObject({
      originalName: 'local',
      name: 'local-renamed',
    })
    expect(host.textContent).toContain('编辑：local-renamed')
  })

  it('keeps invalid edits in place without writing', async () => {
    await mount()
    await edit()
    await typeName('')
    await act(async () => {
      nameInput().focus()
      nameInput().blur()
      await Promise.resolve()
    })
    expect(api.saveCustomModel).not.toHaveBeenCalled()
    expect(host.textContent).toContain('名称不能为空')
  })
})
