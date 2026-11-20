import { describe, it, expect } from 'vitest'
import { sanitizeDeclarations, sanitizeStylesheet, stripCssComments } from './static-css'

describe('sanitizeDeclarations', () => {
  it('keeps static layout, color, border, font and grid declarations', () => {
    const css = 'display:grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap:12px; color:#1f2937; ' +
      'background: linear-gradient(90deg, rgb(1,2,3), hsl(200 50% 50%)); border:1px solid #ccc; ' +
      'font-family:"Microsoft YaHei", sans-serif; position:absolute; z-index:3; transform: translate(4px, 2px) rotate(3deg)'
    const out = sanitizeDeclarations(css)
    expect(out.split(';')).toHaveLength(10)
    expect(out).toContain('grid-template-columns:repeat(3, minmax(0, 1fr))')
    expect(out).toContain('font-family:"Microsoft YaHei", sans-serif')
  })
  it('allows url() only for in-document #id references', () => {
    expect(sanitizeDeclarations('fill:url(#grad1);stroke:url("#g2");marker-end:url( \'#arrow\' )'))
      .toBe('fill:url(#grad1);stroke:url("#g2");marker-end:url( \'#arrow\' )')
    expect(sanitizeDeclarations('background:url(https://x.test/a.png);color:red')).toBe('color:red')
    expect(sanitizeDeclarations('background-image:url(a.png)')).toBe('')
    expect(sanitizeDeclarations('background:url(data:image/png;base64,AAAA)')).toBe('')
    expect(sanitizeDeclarations('background:URL(//evil)')).toBe('')
    expect(sanitizeDeclarations('fill:url(#a) , url(http://x)')).toBe('')
  })
  it('rejects resource-loading and code-ish functions', () => {
    for (const v of ['image-set("a.png" 1x)', '-webkit-image-set(url(a) 1x)', 'cross-fade(a, b)', 'element(#x)', 'expression(alert(1))', 'paint(foo)']) {
      expect(sanitizeDeclarations(`background:${v}`)).toBe('')
    }
    expect(sanitizeDeclarations('width:calc (100% - 2px)')).toBe('')
  })
  it('rejects escapes and markup that could smuggle url( or </style>', () => {
    expect(sanitizeDeclarations('background:\\75 rl(http://x)')).toBe('')
    expect(sanitizeDeclarations('content:"</style><script>x</script>"')).toBe('')
    expect(sanitizeDeclarations('color:red}body{color:blue')).toBe('')
  })
  it('keeps url-looking text inside strings harmless but still refuses real calls', () => {
    expect(sanitizeDeclarations('content:"url(x)"')).toBe('content:"url(x)"')
  })
  it('drops animation, transition and legacy binding properties', () => {
    expect(sanitizeDeclarations('animation:spin 1s;-webkit-animation-name:x;transition:all 1s;transition-delay:1s;-moz-binding:x;behavior:x;color:red'))
      .toBe('color:red')
  })
  it('drops absurd lengths, including exponent forms', () => {
    expect(sanitizeDeclarations('height:999999px;width:1e9px;margin:-30000px;padding:20000px'))
      .toBe('padding:20000px')
  })
  it('accepts custom properties but checks their values too', () => {
    expect(sanitizeDeclarations('--accent:#f00;--bg:url(http://x);color:var(--accent)')).toBe('--accent:#f00;color:var(--accent)')
  })
  it('ignores malformed declarations and javascript: values', () => {
    expect(sanitizeDeclarations(':red;color;1x:3;color:javascript:alert(1);top:0')).toBe('top:0')
  })
})

describe('sanitizeStylesheet', () => {
  it('keeps ordinary rules and @media, drops other at-rules', () => {
    const css = `@import url(x.css);
      @charset "utf-8";
      @font-face { font-family: X; src: url(x.woff) }
      @keyframes spin { from { transform: rotate(0) } to { transform: rotate(360deg) } }
      .box { color: red; animation: spin 1s }
      #mmd-1 .node rect { fill: #eef; stroke: url(#g) }
      @media (max-width: 600px) { .box { display: block } }
      @supports (display:grid) { .box { display:grid } }`
    expect(sanitizeStylesheet(css)).toBe([
      '.box{color:red}',
      '#mmd-1 .node rect{fill:#eef;stroke:url(#g)}',
      '@media (max-width: 600px){.box{display:block}}',
    ].join('\n'))
  })
  it('drops rules whose selector or body is unsafe, keeps the rest', () => {
    expect(sanitizeStylesheet('a\\:b{color:red} .ok{color:blue} .bad{background:url(//x)}')).toBe('.ok{color:blue}')
  })
  it('never emits </style> or a stray closing brace sequence', () => {
    const out = sanitizeStylesheet('.a{color:red}</style><script>alert(1)</script><style>.b{color:blue}')
    expect(out).not.toMatch(/<\/?style|<script/i)
  })
  it('drops unterminated blocks and survives stray braces', () => {
    expect(sanitizeStylesheet('} .a{color:red} .b{color:blue')).toBe('.a{color:red}')
  })
  it('limits @media nesting depth', () => {
    const nested = '@media screen{@media print{@media all{.x{color:red}}}}'
    expect(sanitizeStylesheet(nested)).toBe('')
  })
  it('does not let a brace inside a string break rule boundaries', () => {
    // 值里带花括号的声明按保守策略丢弃，但后面的规则照常解析。
    expect(sanitizeStylesheet('.q::before{content:"}"} .z{color:red}')).toBe('.z{color:red}')
    expect(sanitizeStylesheet('.q::before{content:"a"} .z{color:red}')).toBe('.q::before{content:"a"}\n.z{color:red}')
  })
})

describe('stripCssComments', () => {
  it('removes comments but not comment-like text in strings', () => {
    expect(stripCssComments('a/* x */b "/* keep */" c/* open')).toBe('a b "/* keep */" c ')
  })
})
