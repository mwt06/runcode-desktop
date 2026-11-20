import {
  $createLineBreakNode, $createParagraphNode, $createRangeSelection,
  $createTextNode, $getRoot, $isElementNode, $isLineBreakNode, $isTextNode, $setSelection,
  type LexicalNode, type PointType,
} from 'lexical'

function $text(node: LexicalNode): string {
  if ($isElementNode(node)) return node.getChildren().map($text).join(node.getType() === 'root' ? '\n' : '')
  return node.getTextContent()
}

export function $questionText() { return $text($getRoot()) }

export function $pointOffset(point: PointType): number {
  let offset = 0, found = false
  const walk = (node: LexicalNode) => {
    if (found) return
    if (node.getKey() === point.key) {
      if ($isElementNode(node)) {
        offset += node.getChildren().slice(0, point.offset).reduce((sum, child) => sum + $text(child).length, 0)
        if (node.getType() === 'root') offset += Math.min(point.offset, Math.max(0, node.getChildrenSize() - 1))
      } else offset += point.offset
      found = true
    } else if ($isElementNode(node)) node.getChildren().forEach((child, index) => {
      if (!found && node.getType() === 'root' && index > 0) offset++
      walk(child)
    })
    else offset += $text(node).length
  }
  walk($getRoot())
  return offset
}

export function $selectTextRange(start: number, end = start) {
  const selection = $createRangeSelection()
  const set = (point: PointType, requested: number) => {
    let remaining = Math.max(0, Math.min(requested, $questionText().length)), done = false
    const walk = (node: LexicalNode) => {
      if (done) return
      if ($isTextNode(node)) {
        const size = node.getTextContentSize()
        if (remaining <= size) { point.set(node.getKey(), remaining, 'text'); done = true }
        else remaining -= size
      } else if ($isLineBreakNode(node)) {
        if (remaining === 0) { point.set(node.getParentOrThrow().getKey(), node.getIndexWithinParent(), 'element'); done = true }
        else remaining--
      } else if ($isElementNode(node)) node.getChildren().forEach((child, index) => {
        if (!done && node.getType() === 'root' && index > 0) remaining--
        walk(child)
      })
    }
    walk($getRoot())
    if (!done) {
      const last = $getRoot().getLastChild()
      const parent = $isElementNode(last) ? last : $getRoot()
      point.set(parent.getKey(), parent.getChildrenSize(), 'element')
    }
  }
  set(selection.anchor, start); set(selection.focus, end); $setSelection(selection)
  return selection
}

function $appendText(parent: ReturnType<typeof $createParagraphNode>, text: string) {
  text.split('\n').forEach((line, index) => {
    if (index) parent.append($createLineBreakNode())
    if (line) parent.append($createTextNode(line))
  })
}

export function $replaceQuestion(text: string) {
  const paragraph = $createParagraphNode()
  $getRoot().clear().append(paragraph)
  $appendText(paragraph, text)
}
