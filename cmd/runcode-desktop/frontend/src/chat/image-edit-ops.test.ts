import { describe, expect, it } from 'vitest'
import {
  appendStroke, appendText, fitEditOutputSize, initialEditState, isDirty, redo, sanitizePlainText,
  selectColor, undo, IMAGE_EDIT_MAX_EDGE,
} from './image-edit-ops'

describe('image-edit-ops', () => {
  it('画笔按当前颜色生成 stroke；文字按颜色生成 text', () => {
    let s = selectColor(initialEditState(), '#1971C2')
    s = appendStroke(s, [{ x: 0.1, y: 0.1 }, { x: 0.5, y: 0.5 }])
    expect(s.operations[0]).toMatchObject({ kind: 'stroke', color: '#1971C2' })
    s = selectColor(s, '#E03131')
    s = appendText(s, '标注', 0.2, 0.3)
    expect(s.operations[1]).toMatchObject({ kind: 'text', color: '#E03131', text: '标注', x: 0.2, y: 0.3 })
  })

  it('坐标钳制在 0–1，resize 语义下结果不变', () => {
    const s = appendStroke(initialEditState(), [{ x: -1, y: 2 }, { x: 0.25, y: 0.75 }, { x: 1.5, y: -0.2 }])
    const stroke = s.operations[0]
    expect(stroke?.kind).toBe('stroke')
    if (stroke?.kind !== 'stroke') return
    expect(stroke.points).toEqual([
      { x: 0, y: 1 },
      { x: 0.25, y: 0.75 },
      { x: 1, y: 0 },
    ])
  })

  it('Undo 只撤最近一条；新操作清空 redo', () => {
    let s = appendStroke(initialEditState(), [{ x: 0, y: 0 }, { x: 1, y: 1 }])
    s = appendText(s, 'A', 0.1, 0.1)
    s = undo(s)
    expect(s.operations).toHaveLength(1)
    expect(s.redo).toHaveLength(1)
    s = redo(s)
    expect(s.operations).toHaveLength(2)
    s = undo(s)
    s = appendText(s, 'B', 0.2, 0.2)
    expect(s.redo).toHaveLength(0)
    expect(isDirty(s)).toBe(true)
  })

  it('文字去掉 HTML 尖括号与控制字符', () => {
    expect(sanitizePlainText('  <b>hi</b>\n\tok  ')).toBe('bhi/b ok')
  })

  it('合成尺寸按最长边上限缩放', () => {
    expect(fitEditOutputSize(800, 600)).toEqual({ w: 800, h: 600 })
    const big = fitEditOutputSize(IMAGE_EDIT_MAX_EDGE * 2, IMAGE_EDIT_MAX_EDGE)
    expect(big.w).toBe(IMAGE_EDIT_MAX_EDGE)
    expect(big.h).toBe(IMAGE_EDIT_MAX_EDGE / 2)
  })
})
