// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { parseLength, parseViewBox, sanitizeSvg, SVG_DEFAULT_SIZE, SVG_MAX_DIMENSION } from './static-svg'

function ok(source: string) {
  const r = sanitizeSvg(source)
  if (!r.ok) throw new Error('expected ok, got ' + r.reason)
  return r
}

function parse(svg: string): Element {
  return new DOMParser().parseFromString(svg, 'image/svg+xml').documentElement
}

const ARCH = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 200">
  <defs>
    <marker id="arrow" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto"><path d="M0,0 L9,3 L0,6 z" fill="#333"/></marker>
    <linearGradient id="g"><stop offset="0" stop-color="#eef"/><stop offset="1" stop-color="#ccf"/></linearGradient>
  </defs>
  <rect x="10" y="10" width="120" height="60" rx="8" fill="url(#g)" stroke="#333"/>
  <text x="70" y="45" text-anchor="middle" font-size="14">客户端</text>
  <line x1="130" y1="40" x2="260" y2="40" stroke="#333" marker-end="url(#arrow)"/>
  <rect x="260" y="10" width="120" height="60" rx="8" fill="#fff" stroke="#333"/>
  <text x="320" y="45" text-anchor="middle"><tspan>网关</tspan></text>
</svg>`

describe('sanitizeSvg: keeps static diagrams intact', () => {
  it('preserves shapes, Chinese text, gradients, markers and local url refs', () => {
    const r = ok(ARCH)
    expect(r.width).toBe(400)
    expect(r.height).toBe(200)
    const root = parse(r.svg)
    expect(root.namespaceURI).toBe('http://www.w3.org/2000/svg')
    expect(root.querySelectorAll('rect')).toHaveLength(2)
    expect(root.textContent).toContain('客户端')
    expect(root.textContent).toContain('网关')
    expect(root.querySelector('line')?.getAttribute('marker-end')).toBe('url(#arrow)')
    expect(root.querySelector('rect')?.getAttribute('fill')).toBe('url(#g)')
    expect(root.querySelector('marker')?.getAttribute('refX')).toBe('9')
  })
  it('adds the SVG namespace when the model omitted xmlns', () => {
    const r = ok('<svg viewBox="0 0 10 10"><circle cx="5" cy="5" r="4"/></svg>')
    expect(r.svg).toContain('xmlns="http://www.w3.org/2000/svg"')
    expect(parse(r.svg).querySelector('circle')?.namespaceURI).toBe('http://www.w3.org/2000/svg')
  })
  it('keeps a sanitized <style> element and style attributes', () => {
    const r = ok('<svg viewBox="0 0 10 10"><style>.a{fill:red;animation:x 1s} @import url(x.css);</style><rect class="a" width="5" height="5" style="stroke:blue;background:url(http://x)"/></svg>')
    const root = parse(r.svg)
    expect(root.querySelector('style')?.textContent).toBe('.a{fill:red}')
    expect(root.querySelector('rect')?.getAttribute('style')).toBe('stroke:blue')
  })
  it('unwraps links but keeps their drawing', () => {
    const r = ok('<svg viewBox="0 0 10 10"><a href="https://evil.test"><rect width="5" height="5"/></a></svg>')
    const root = parse(r.svg)
    expect(root.querySelector('a')).toBeNull()
    expect(root.querySelector('rect')).not.toBeNull()
    expect(r.svg).not.toContain('evil.test')
  })
  it('converts xlink:href to href for local references only', () => {
    const r = ok('<svg xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 10 10"><defs><circle id="c" r="2"/></defs><use xlink:href="#c"/><use href="other.svg#c"/></svg>')
    const uses = parse(r.svg).querySelectorAll('use')
    expect(uses).toHaveLength(1)
    expect(uses[0].getAttribute('href')).toBe('#c')
  })
  it('allows only small inline raster data in <image>', () => {
    const r = ok('<svg viewBox="0 0 10 10"><image href="data:image/png;base64,iVBORw0KGgo=" width="5" height="5"/><image href="https://x.test/a.png"/><image href="data:image/svg+xml;base64,PHN2Zz4="/><rect width="1" height="1"/></svg>')
    const images = parse(r.svg).querySelectorAll('image')
    expect(images).toHaveLength(1)
    expect(images[0].getAttribute('href')).toMatch(/^data:image\/png/)
  })
})

describe('sanitizeSvg: strips active and external content (X01/X03/X05)', () => {
  it('removes script, event handlers, foreignObject, animation and javascript URLs', () => {
    const evil = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10" onload="alert(1)">
      <script>alert(1)</script>
      <foreignObject width="10" height="10"><div xmlns="http://www.w3.org/1999/xhtml">x<img src="x" onerror="alert(2)"/></div></foreignObject>
      <rect width="5" height="5" onclick="alert(3)" fill="url(http://evil/x)"><animate attributeName="x" to="1"/><set attributeName="fill" to="red"/></rect>
      <a href="javascript:alert(4)"><text>t</text></a>
      <iframe src="https://evil"/>
    </svg>`
    const r = ok(evil)
    expect(r.svg).not.toMatch(/script|onload|onclick|onerror|foreignObject|animate|<set|javascript|iframe|evil|<div|<img/i)
    expect(parse(r.svg).querySelector('rect')?.hasAttribute('fill')).toBe(false)
  })
  it('drops unknown attributes, namespaced junk and markup-looking values', () => {
    const r = ok('<svg viewBox="0 0 10 10" xmlns:ev="http://www.w3.org/2001/xml-events"><rect width="5" height="5" data-x="1" ev:event="click" fill="&lt;b&gt;" class="ok"/></svg>')
    const rect = parse(r.svg).querySelector('rect')!
    expect(rect.getAttribute('class')).toBe('ok')
    expect(rect.hasAttribute('data-x')).toBe(false)
    expect(rect.hasAttribute('fill')).toBe(false)
    expect(r.svg).not.toContain('xml-events')
  })
  it('catches javascript: split by whitespace or control characters', () => {
    const r = ok('<svg viewBox="0 0 10 10"><rect width="5" height="5" fill="java&#9;script:alert(1)" stroke=" JavaScript :x"/></svg>')
    expect(r.svg).not.toMatch(/script/i)
  })
  it('drops elements from foreign namespaces', () => {
    const r = ok('<svg xmlns="http://www.w3.org/2000/svg" xmlns:h="http://www.w3.org/1999/xhtml" viewBox="0 0 10 10"><h:script>alert(1)</h:script><rect width="1" height="1"/></svg>')
    expect(r.svg).not.toContain('alert')
  })
})

describe('sanitizeSvg: failure modes fall back to source', () => {
  it('rejects broken XML, non-svg roots, empty drawings and DTD tricks', () => {
    expect(sanitizeSvg('<svg><rect></svg>')).toEqual({ ok: false, reason: 'parse-error' })
    expect(sanitizeSvg('<html><body/></html>')).toEqual({ ok: false, reason: 'not-svg' })
    expect(sanitizeSvg('<svg viewBox="0 0 1 1"><script>x</script></svg>')).toEqual({ ok: false, reason: 'empty' })
    expect(sanitizeSvg('<svg viewBox="0 0 1 1"><text>  </text></svg>')).toEqual({ ok: false, reason: 'empty' })
    expect(sanitizeSvg('<!DOCTYPE svg [<!ENTITY a "aaaa">]><svg><text>&a;</text></svg>')).toEqual({ ok: false, reason: 'unsafe-dtd' })
  })
  it('rejects oversized and overly complex input', () => {
    expect(sanitizeSvg('<svg>' + ' '.repeat(256 * 1024) + '</svg>')).toEqual({ ok: false, reason: 'too-large' })
    const filters = '<feGaussianBlur stdDeviation="1"/>'.repeat(33)
    expect(sanitizeSvg(`<svg viewBox="0 0 1 1"><filter id="f">${filters}</filter><rect width="1" height="1"/></svg>`)).toEqual({ ok: false, reason: 'too-complex' })
  })
})

describe('sanitizeSvg: sizing (L04)', () => {
  it('derives viewBox from absolute width/height', () => {
    const r = ok('<svg width="300" height="150px"><rect width="10" height="10"/></svg>')
    expect([r.width, r.height]).toEqual([300, 150])
    expect(parse(r.svg).getAttribute('viewBox')).toBe('0 0 300 150')
  })
  it('fills the missing dimension from the viewBox ratio, ignoring percentages', () => {
    const r = ok('<svg width="100%" height="200" viewBox="0 0 800 400"><rect width="10" height="10"/></svg>')
    expect([r.width, r.height]).toEqual([400, 200])
  })
  it('uses a bounded default when there is no size information at all', () => {
    const r = ok('<svg><rect width="10" height="10"/></svg>')
    expect([r.width, r.height]).toEqual([SVG_DEFAULT_SIZE.width, SVG_DEFAULT_SIZE.height])
  })
  it('scales huge intrinsic sizes down to the max dimension, keeping the ratio', () => {
    const r = ok('<svg viewBox="0 0 100000 50000"><rect width="10" height="10"/></svg>')
    expect([r.width, r.height]).toEqual([SVG_MAX_DIMENSION, SVG_MAX_DIMENSION / 2])
  })
})

describe('length helpers', () => {
  it('parseLength accepts unitless and px only', () => {
    expect(parseLength('12')).toBe(12)
    expect(parseLength(' 12.5px ')).toBe(12.5)
    expect(parseLength('50%')).toBeNull()
    expect(parseLength('2em')).toBeNull()
    expect(parseLength('-3')).toBeNull()
    expect(parseLength('0')).toBeNull()
  })
  it('parseViewBox requires four finite numbers with positive size', () => {
    expect(parseViewBox('0 0 10 20')).toEqual({ x: 0, y: 0, w: 10, h: 20 })
    expect(parseViewBox('0,0,10,20')).toEqual({ x: 0, y: 0, w: 10, h: 20 })
    expect(parseViewBox('0 0 0 20')).toBeNull()
    expect(parseViewBox('0 0 10')).toBeNull()
  })
})
