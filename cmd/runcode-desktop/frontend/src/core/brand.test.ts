import { describe, expect, it } from 'vitest'
import { selectBrand } from './brand'

describe('selectBrand', () => {
  it('选中已知品牌', () => {
    expect(selectBrand('zhikai').name).toBe('智开')
    expect(selectBrand('runcode').name).toBe('XRUN')
  })

  it('空、未设、拼错都回落默认品牌(原品牌保留)', () => {
    expect(selectBrand(undefined).key).toBe('runcode')
    expect(selectBrand('').key).toBe('runcode')
    expect(selectBrand('  ').key).toBe('runcode')
    expect(selectBrand('zhikaii').key).toBe('runcode')
  })

  it('容忍开关值首尾空白', () => {
    expect(selectBrand(' zhikai ').name).toBe('智开')
  })

  it('每套品牌都自带匹配的标记、文案与欢迎语形态', () => {
    const zhikai = selectBrand('zhikai')
    expect(zhikai.logo.kind).toBe('image')
    expect(zhikai.tagline).toContain('办公')
    expect(zhikai.loginHeadline).toContain('办公')
    expect(zhikai.greeting).toBe('welcome')

    const runcode = selectBrand('runcode')
    expect(runcode.logo.kind).toBe('mark')
    expect(runcode.tagline).toContain('编程')
    expect(runcode.greeting).toBe('explore')
  })
})
