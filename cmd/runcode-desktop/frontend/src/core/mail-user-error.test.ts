import { describe, expect, it } from 'vitest'
import { formatMailUserError } from './mail-user-error'

describe('formatMailUserError', () => {
  it('maps empty body / subject / recipients', () => {
    expect(formatMailUserError('mail: invalid input: empty body')).toBe('请填写正文内容')
    expect(formatMailUserError('mail: invalid input: empty subject')).toBe('请填写主题')
    expect(formatMailUserError('mail: invalid input: need recipients')).toBe('请至少填写一位收件人')
  })

  it('maps bad address and keeps Chinese backend text', () => {
    expect(formatMailUserError('mail: invalid input: bad address')).toBe('收件人邮箱格式不正确')
    expect(formatMailUserError('mail: invalid input: 草稿已被其它位置更新，请刷新后重试'))
      .toBe('草稿已被其它位置更新，请刷新后重试')
  })

  it('falls back for unknown text', () => {
    expect(formatMailUserError('network down')).toBe('network down')
    expect(formatMailUserError('')).toBe('操作失败')
  })
})
