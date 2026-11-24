import { afterEach, describe, expect, it, vi } from 'vitest'
import { logPreviewFailure, previewFailureHint, previewFailureLabel } from './preview-failure'

describe('previewFailureLabel', () => {
  it('maps known codes to Chinese phrases', () => {
    expect(previewFailureLabel('unsupported-type')).toBe('暂不支持此图型')
    expect(previewFailureLabel('platform-unsupported')).toBe('当前环境不支持该预览')
    expect(previewFailureLabel('needs-script')).toContain('未能加载的脚本')
    expect(previewFailureLabel('syntax')).toBe('语法无法解析')
    expect(previewFailureLabel('timeout')).toBe('渲染超时')
    expect(previewFailureLabel('busy')).toBe('预览队列繁忙')
    expect(previewFailureLabel('cancelled')).toBe('已取消')
    expect(previewFailureLabel('load-failed')).toBe('渲染器加载失败')
    expect(previewFailureLabel('unsafe-output')).toBe('输出未通过安全检查')
  })
  it('falls back for unknown codes', () => {
    expect(previewFailureLabel('weird')).toBe('预览不可用')
    expect(previewFailureLabel(null)).toBe('预览不可用')
  })
  it('dev hint appends the code', () => {
    const hint = previewFailureHint('syntax')
    expect(hint).toContain('语法无法解析')
  })
})

describe('logPreviewFailure', () => {
  afterEach(() => {
    vi.unstubAllEnvs()
    vi.restoreAllMocks()
  })

  it('does not call console.debug when DEV is false (production)', () => {
    vi.stubEnv('DEV', false)
    const spy = vi.spyOn(console, 'debug').mockImplementation(() => {})
    logPreviewFailure({ kind: 'mermaid', reason: 'syntax', messageId: 'm1' })
    expect(spy).not.toHaveBeenCalled()
  })

  it('calls console.debug in development builds', () => {
    vi.stubEnv('DEV', true)
    const spy = vi.spyOn(console, 'debug').mockImplementation(() => {})
    logPreviewFailure({ kind: 'svg', reason: 'parse-error', messageId: 'm2' })
    expect(spy).toHaveBeenCalled()
  })
})
