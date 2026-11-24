// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { MailSafePreview } from './safe-preview'

let host: HTMLDivElement
let root: Root

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

describe('MailSafePreview', () => {
  it('strips image markdown so remote images are not loaded', async () => {
    await act(async () => {
      root.render(<MailSafePreview markdown={'你好 ![x](https://evil.example/a.png) 结束'} />)
    })
    expect(host.textContent).toContain('图片已省略')
    expect(host.querySelector('img')).toBeNull()
    expect(host.textContent).toContain('你好')
    expect(host.textContent).toContain('结束')
  })
})
