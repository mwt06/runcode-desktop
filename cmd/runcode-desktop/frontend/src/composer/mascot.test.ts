import { describe, expect, it } from 'vitest'
import { nextIdleDelayMS } from './mascot'

describe('nextIdleDelayMS', () => {
  it('落在 45s..120s 之间', () => {
    expect(nextIdleDelayMS(() => 0)).toBe(45_000)
    expect(nextIdleDelayMS(() => 0.999999)).toBe(120_000)
    expect(nextIdleDelayMS(() => 0.5)).toBeGreaterThan(45_000)
    expect(nextIdleDelayMS(() => 0.5)).toBeLessThan(120_000)
  })

  it('下界明显长过一次播放（约 4.8 秒），否则观感上还是连着放', () => {
    for (let i = 0; i <= 10; i++) {
      expect(nextIdleDelayMS(() => i / 10)).toBeGreaterThan(10_000)
    }
  })

  it('随机源越界也不会跑出区间', () => {
    expect(nextIdleDelayMS(() => -1)).toBe(45_000)
    expect(nextIdleDelayMS(() => 5)).toBe(120_000)
  })

  it('不是固定值——不同随机源给出不同间隔，避免可预期的节拍', () => {
    const a = nextIdleDelayMS(() => 0.1)
    const b = nextIdleDelayMS(() => 0.8)
    expect(a).not.toBe(b)
  })
})
