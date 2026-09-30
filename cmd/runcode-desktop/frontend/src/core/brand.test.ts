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

  it('录音纪要：原品牌开着，智开版临时关掉', () => {
    // 这条盯的是「临时下线只影响智开」：原品牌 XRUN 被顺带关掉的话，这里先红。
    expect(selectBrand('runcode').features.recorder).toBe(true)
    expect(selectBrand('zhikai').features.recorder).toBe(false)
  })
})


describe('国开版品牌', () => {
  it('独立身份沿用智开视觉，显式选择国开场景且不开放录音', () => {
    const brand = selectBrand('zhikai-guokai')
    const standard = selectBrand('zhikai')
    expect(brand.key).toBe('zhikai-guokai')
    expect(brand.name).toBe('智开（国开版）')
    expect(brand.loginHeadline).toContain('国开版')
    expect(brand.logo.kind).toBe('image')
    expect(brand.greeting).toBe('welcome')
    expect(brand.greetingMark).toEqual(standard.greetingMark)
    expect(brand.composerMark).toEqual(standard.composerMark)
    expect(brand.features.recorder).toBe(false)
    expect(brand.scenarioProfile).toBe('guokai')
    expect(standard.scenarioProfile).toBe('full')
    expect(selectBrand('runcode').scenarioProfile).toBe('full')
  })
})
