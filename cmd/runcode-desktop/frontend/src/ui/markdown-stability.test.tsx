// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it } from 'vitest'
import { Markdown } from './markdown'

let host: HTMLDivElement
let root: Root

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

it('父级重渲染保持 Markdown 组件身份，不重挂载先前回复', async () => {
  const draw = () => root.render(<Markdown>hello **world**</Markdown>)
  await act(async () => draw())
  const paragraph = host.querySelector('p')
  const strong = host.querySelector('strong')
  expect(paragraph).toBeTruthy()
  expect(strong).toBeTruthy()

  await act(async () => draw())
  expect(host.querySelector('p')).toBe(paragraph)
  expect(host.querySelector('strong')).toBe(strong)
})
