import { forwardRef, useImperativeHandle, useLayoutEffect, useMemo, useRef, type ClipboardEventHandler, type CSSProperties, type KeyboardEventHandler, type MouseEventHandler, type RefObject } from 'react'
import { LexicalComposer } from '@lexical/react/LexicalComposer'
import { useLexicalComposerContext } from '@lexical/react/LexicalComposerContext'
import { ContentEditable } from '@lexical/react/LexicalContentEditable'
import { PlainTextPlugin } from '@lexical/react/LexicalPlainTextPlugin'
import { HistoryPlugin } from '@lexical/react/LexicalHistoryPlugin'
import { createEmptyHistoryState } from '@lexical/history'
import { LexicalErrorBoundary } from '@lexical/react/LexicalErrorBoundary'
import { mergeRegister } from '@lexical/utils'
import { $createParagraphNode, $getRoot, $getSelection, $isRangeSelection, CLEAR_HISTORY_COMMAND, COMMAND_PRIORITY_HIGH, DRAGSTART_COMMAND, DROP_COMMAND, HISTORY_MERGE_TAG, HISTORY_PUSH_TAG, SKIP_DOM_SELECTION_TAG, SKIP_SELECTION_FOCUS_TAG, type RangeSelection } from 'lexical'
import { isComposingKey } from '@/ui/keys'
import { $pointOffset, $questionText, $replaceQuestion, $selectTextRange } from './inline-editor-model'
import './inline-editor.css'

export interface ComposerEditorHandle {
  focus(): void
  getSelection(): { start: number; end: number }
    setSelectionRange(start: number, end: number): void
}

export interface InlineEditorProps {
  value: string
  onChange: (text: string, caret: number) => void
  readOnly: boolean
  elementRef: RefObject<HTMLDivElement>
  id: string
  placeholder: string
  className: string
  style?: CSSProperties
  onClick?: MouseEventHandler<HTMLDivElement>
  onPaste?: ClipboardEventHandler<HTMLDivElement>
  onKeyDown?: KeyboardEventHandler<HTMLDivElement>
  onKeyUp?: KeyboardEventHandler<HTMLDivElement>
}

const EXTERNAL_UPDATE = 'composer-external'

const EditorSurface = forwardRef<ComposerEditorHandle, InlineEditorProps>(function EditorSurface(props, ref) {
  const [editor] = useLexicalComposerContext()
  const history = useMemo(createEmptyHistoryState, [])
  const latest = useRef(props); latest.current = props
  const output = useRef({ text: props.value })
  const savedSelection = useRef<RangeSelection | null>(null)
  const caret = useRef({ start: 0, end: 0 })

  useImperativeHandle(ref, () => ({
    focus: () => editor.focus(),
    getSelection: () => caret.current,
    setSelectionRange: (start, end) => editor.update(() => { $selectTextRange(start, end) }, { discrete: true }),
  }), [editor])

  useLayoutEffect(() => mergeRegister(
    editor.registerUpdateListener(({ editorState, tags }) => {
      editorState.read(() => {
        const selection = $getSelection()
        if ($isRangeSelection(selection)) {
          savedSelection.current = selection.clone()
          const anchor = $pointOffset(selection.anchor), focus = $pointOffset(selection.focus)
          caret.current = { start: Math.min(anchor, focus), end: Math.max(anchor, focus) }
        }
        const text = $questionText()
        const changedText = text !== output.current.text
        output.current = { text }
        if (!tags.has(EXTERNAL_UPDATE) && changedText) latest.current.onChange(text, caret.current.end)
      })
    }),
    editor.registerCommand(DRAGSTART_COMMAND, event => { event.preventDefault(); return true }, COMMAND_PRIORITY_HIGH),
    editor.registerCommand(DROP_COMMAND, event => { event.preventDefault(); return true }, COMMAND_PRIORITY_HIGH),
  ), [editor, history])

  useLayoutEffect(() => { editor.setEditable(!props.readOnly) }, [editor, props.readOnly])

  useLayoutEffect(() => {
    const textChanged = props.value !== output.current.text
    if (!textChanged) return
    editor.update(() => {
      const previousCaret = { ...caret.current }
      $replaceQuestion(props.value)
      $selectTextRange(previousCaret.start, previousCaret.end)
    }, { tag: [EXTERNAL_UPDATE, HISTORY_MERGE_TAG, SKIP_SELECTION_FOCUS_TAG, ...(document.activeElement === props.elementRef.current ? [] : [SKIP_DOM_SELECTION_TAG])], discrete: true })
    editor.dispatchCommand(CLEAR_HISTORY_COMMAND, undefined)
  }, [editor, props.value, props.elementRef])

  const keyDown: KeyboardEventHandler<HTMLDivElement> = event => {
    props.onKeyDown?.(event)
    if (event.defaultPrevented) event.stopPropagation()
    else if (isComposingKey(event)) event.stopPropagation()
  }
  const paste: ClipboardEventHandler<HTMLDivElement> = event => {
    props.onPaste?.(event)
    if (event.defaultPrevented) { event.stopPropagation(); return }
    event.preventDefault(); event.stopPropagation()
    if (props.readOnly) return
    const text = event.clipboardData.getData('text/plain')
    editor.update(() => { const selection = $getSelection(); if ($isRangeSelection(selection)) selection.insertRawText(text) }, { tag: HISTORY_PUSH_TAG })
  }

  return <>
    <PlainTextPlugin
      contentEditable={<ContentEditable ref={props.elementRef} id={props.id} className={`composer-inline-editor ${props.className}`} style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', ...props.style }} aria-label={props.placeholder} aria-multiline="true" aria-readonly={props.readOnly} data-placeholder={props.placeholder} onClick={props.onClick} onKeyDownCapture={keyDown} onKeyUp={props.onKeyUp} onPasteCapture={paste} />}
      placeholder={null}
      ErrorBoundary={LexicalErrorBoundary}
    />
    <HistoryPlugin externalHistoryState={history} />
  </>
})

export const InlineEditor = forwardRef<ComposerEditorHandle, InlineEditorProps>(function InlineEditor(props, ref) {
  return <LexicalComposer initialConfig={{ namespace: 'composer', nodes: [], editable: !props.readOnly, onError: error => { throw error }, editorState: () => {
    $getRoot().append($createParagraphNode())
    $replaceQuestion(props.value)
  } }}><EditorSurface {...props} ref={ref} /></LexicalComposer>
})
