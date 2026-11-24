import { describe, expect, it } from 'vitest'
import { toolIcon } from './icons'

describe('toolIcon', () => {
  it('uses the picture-plus glyph for image generation', () => {
    expect(toolIcon('image_generate')).toBe('image-plus')
  })
})
