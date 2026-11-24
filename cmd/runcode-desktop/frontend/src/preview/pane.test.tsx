// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { PreviewPane } from './pane'
import type { PreviewTab } from './tabs'

let root: Root
let host: HTMLDivElement

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  vi.stubGlobal('URL', {
    ...URL,
    createObjectURL: vi.fn(() => 'blob:inline-preview'),
    revokeObjectURL: vi.fn(),
  })
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  })
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.unstubAllGlobals()
})

it('renders an inline-static code preview inside the right preview pane', async () => {
  const tab: PreviewTab = {
    kind: 'inline-static',
    ownerKey: 'inline:S:m1:0:10',
    title: '架构图',
    sourceKind: 'svg',
    source: '<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>',
    messageID: 'm1',
    sessionID: 'S',
  }
  await act(async () => root.render(
    <PreviewPane
      tabs={[tab]}
      active={tab.ownerKey}
      baseURL=""
      onSelect={vi.fn()}
      onClose={vi.fn()}
      onCloseTab={vi.fn()}
    />,
  ))
  await act(async () => { await Promise.resolve() })
  expect(host.textContent).toContain('架构图')
  expect(host.querySelector('img')?.getAttribute('src')).toBe('blob:inline-preview')
})
