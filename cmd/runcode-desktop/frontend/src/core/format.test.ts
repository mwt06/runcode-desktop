import { describe, expect, it } from 'vitest'
import { fmtBytes, fmtDuration, fmtTokens } from './format'

describe('fmtBytes', () => {
  it('按二进制进位，好和资源管理器里的大小对得上', () => {
    expect(fmtBytes(512)).toBe('512 B')
    expect(fmtBytes(1024)).toBe('1.0 KB')
    expect(fmtBytes(84213760)).toBe('80.3 MB')
    expect(fmtBytes(2 * 1024 ** 3)).toBe('2.0 GB')
  })

  // 0 的含义是「服务端没给 Content-Length」，不是「这个文件是空的」。
  // 画成 "0 B" 会让用户以为要下的是个空文件，所以这里必须是空串——
  // 界面据此改画不确定态的进度条。
  it('长度未知时给空串而不是 0 B', () => {
    expect(fmtBytes(0)).toBe('')
    expect(fmtBytes(undefined)).toBe('')
    expect(fmtBytes(-1)).toBe('')
  })
})

describe('fmtTokens', () => {
  it('紧凑渲染 token 数', () => {
    expect(fmtTokens(340)).toBe('340')
    expect(fmtTokens(1234)).toBe('1.2k')
    expect(fmtTokens(23000)).toBe('23k')
  })
})

describe('fmtDuration', () => {
  it('紧凑渲染时长', () => {
    expect(fmtDuration(850)).toBe('0.8s')
    expect(fmtDuration(3200)).toBe('3.2s')
    expect(fmtDuration(75000)).toBe('1m15s')
    expect(fmtDuration(0)).toBe('')
  })
})
