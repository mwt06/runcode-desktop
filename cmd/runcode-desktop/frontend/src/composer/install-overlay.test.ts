import { describe, expect, it } from 'vitest'
import { downloadPercent, formatBytes } from './install-overlay'

describe('formatBytes', () => {
  it('按量级换单位', () => {
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(2048)).toBe('2 KB')
    expect(formatBytes(3 * 1024 * 1024)).toBe('3.0 MB')
  })

  it('零字节也读得通', () => {
    // 下载刚开始那一帧就是 0，不能显示成 "NaN" 或空白。
    expect(formatBytes(0)).toBe('0 B')
  })
})

describe('downloadPercent', () => {
  it('总长未知时返回 null，交给不确定态', () => {
    // 服务端没给 Content-Length（分块传输）时后端把 total 归零。拿 0 去做除数
    // 会算出 Infinity，进度环直接画飞。
    expect(downloadPercent(1234, 0)).toBeNull()
    expect(downloadPercent(1234, -1)).toBeNull()
  })

  it('封顶 99，不让它到 100', () => {
    // 下载完还有校验和解压。先到 100% 再停在那儿不动，比停在 97% 更像卡死了。
    expect(downloadPercent(1000, 1000)).toBe(99)
    expect(downloadPercent(9999, 1000)).toBe(99)
  })

  it('正常区间向下取整', () => {
    expect(downloadPercent(500, 1000)).toBe(50)
    expect(downloadPercent(1, 1000)).toBe(0)
  })

  it('负数收到 0', () => {
    expect(downloadPercent(-5, 1000)).toBe(0)
  })
})
