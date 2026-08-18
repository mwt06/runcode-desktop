import { describe, expect, it } from 'vitest'
import type { CustomModel } from '@/core/bridge'
import { canAutoStartCustom, initialModelChoice } from './restore'

const cm = (name: string): CustomModel => ({ name, model: name, baseURL: 'https://x/v1' })

describe('initialModelChoice', () => {
  it('恢复自定义连接：存的是 customModelName + 实际 provider，不是 passport', () => {
    // 这正是线上配置的形状（provider=anthropic + customModelName=claude-opus-5）,
    // 旧实现在这里返回空串,导致每次启动都要重选模型。
    expect(initialModelChoice({ provider: 'anthropic', model: 'claude-opus-5', customModelName: 'claude-opus-5' }))
      .toBe('custom:claude-opus-5')
    expect(initialModelChoice({ provider: 'openai', customModelName: 'k3' })).toBe('custom:k3')
  })

  it('恢复平台模型', () => {
    expect(initialModelChoice({ provider: 'passport', model: 'model-1' })).toBe('passport:model-1')
  })

  it('customModelName 优先于 provider——两者并存时它才是真正被选中的那个', () => {
    expect(initialModelChoice({ provider: 'passport', model: 'model-1', customModelName: 'k3' })).toBe('custom:k3')
  })

  it('没有可恢复的选择时返回空串', () => {
    expect(initialModelChoice({})).toBe('')
    expect(initialModelChoice({ provider: 'passport' })).toBe('')
    expect(initialModelChoice({ provider: 'openai', model: 'gpt-x' })).toBe('')   // 非 passport 且无连接档名
    expect(initialModelChoice({ customModelName: '   ' })).toBe('')
  })
})

describe('canAutoStartCustom', () => {
  const models = [cm('claude-opus-5'), cm('k3')]

  it('工作区与连接档都还在就可以直接进', () => {
    expect(canAutoStartCustom({ cwd: 'D:/ws', customModelName: 'k3' }, models)).toBe(true)
  })

  it('不依赖登录态与租户——这是它与通行证路径的区别', () => {
    // 入参里根本没有账号快照可言：判定只看 cwd 与连接档。
    expect(canAutoStartCustom({ cwd: 'D:/ws', customModelName: 'claude-opus-5' }, models)).toBe(true)
  })

  it('连接档被删掉或改名后不再自动进入，让用户回表单重选', () => {
    expect(canAutoStartCustom({ cwd: 'D:/ws', customModelName: '已删掉的' }, models)).toBe(false)
    expect(canAutoStartCustom({ cwd: 'D:/ws', customModelName: 'k3' }, [])).toBe(false)
  })

  it('缺工作区或不是自定义连接时不自动进入', () => {
    expect(canAutoStartCustom({ customModelName: 'k3' }, models)).toBe(false)
    expect(canAutoStartCustom({ cwd: '   ', customModelName: 'k3' }, models)).toBe(false)
    expect(canAutoStartCustom({ cwd: 'D:/ws', provider: 'passport', model: 'model-1' }, models)).toBe(false)
  })
})
