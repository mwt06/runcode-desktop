// @vitest-environment jsdom
import { act, createRef } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { getNearestEditorFromDOMNode } from 'lexical'
import { InlineEditor, type ComposerEditorHandle } from './inline-editor'

describe('inline composer editor', () => {
  let host: HTMLDivElement, root: Root

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

  it('renders controlled text', async () => {
    const handle = createRef<ComposerEditorHandle>()
    const element = createRef<HTMLDivElement>()
    await act(async () => {
      root.render(
        <InlineEditor
          ref={handle}
          elementRef={element}
          id="test-input"
          placeholder="请输入"
          className=""
          value="hello"
          readOnly={false}
          onChange={() => {}}
        />,
      )
    })
    const textbox = host.querySelector<HTMLElement>('[role="textbox"]')!
    const editor = getNearestEditorFromDOMNode(textbox)!
  editor.getEditorState().read(() => {
      expect(textbox.textContent).toContain('hello')
    })
  })
})