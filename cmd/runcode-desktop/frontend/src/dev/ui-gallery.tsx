// 公共组件画廊：把 ui/ 下的每个共用件和全部设计 token 摆在一页上。
// 由 main.tsx 按 ?preview=ui 挂载，正常流程不会用到。
//
// 它同时是**视觉回归的检查台**：样式收敛（token 化、字号并档、圆角合并）这类改动
// 靠 tsc/eslint/vitest 是证明不了观感的，改完开这一页扫一眼最快。
// 配套的文字说明见 ui/README.md——那边讲"何时用哪个"，这边讲"长什么样"。
import { useState } from 'react'
import { Banner, InlineError, SystemNote, type Tone } from '@/ui/feedback'
import { PreviewLoading, PreviewSkeleton } from '@/preview/loading'
import { INSET_BOX, InsetRow, PageShell, Placeholder } from '@/ui/layout'
import { BTN, BTN_DANGER, BTN_PRIMARY } from '@/ui/tokens'
import { FIELD_CLS, LABEL_CLS, SelectField } from '@/ui/fields'
import { DiffStat, SourceBadge } from '@/ui/badges'
import { CheckMark, Spinner, WarnTriangle } from '@/ui/glyphs'
import { CollapsibleGroup } from '@/ui/collapsible-group'
import { ConfirmDialog } from '@/ui/confirm-dialog'
import { GhostBtn } from '@/ui/ghost-btn'
import { HScroll } from '@/ui/h-scroll'
import { Icon } from '@/ui/icons'
import { Popover } from '@/ui/popover'
import { Toggle } from '@/ui/toggle'

const TONES: Tone[] = ['neutral', 'warning', 'danger']

const COLORS: [string, string][] = [
  ['bg', '页面底'], ['surface', '卡片面'], ['surface2', '次级面'], ['inset', '嵌入面'],
  ['line', '浅线'], ['line2', '常规边框'],
  ['ink', '标题墨'], ['ink2', '正文墨'], ['muted', '次要'], ['faint', '更次要'],
  ['primary', '主色'], ['primaryink', '主色文字'], ['primarysoft', '主色浅底'], ['userbg', '用户气泡'],
  ['green', '成功'], ['greenbg', '成功浅底'], ['red', '失败'], ['redbg', '失败浅底'],
  ['amber', '警告标记'], ['amberink', '警告文字'],
]
const RADII = ['field', 'btn', 'card', 'md', 'lg', 'xl', '2xl', 'full']
// 阴影必须写成整类名的字面量数组，不能像圆角那样用 var(--shadow-…)：
// Tailwind v4 为了支持 shadow-color 修饰符，把阴影值**内联展开**进 utility
// （`.shadow-focus{--tw-shadow:0 0 0 3px …}`），并不会把 --shadow-* 输出到 :root，
// 所以 var(--shadow-focus) 取到的是空值。--radius-* 则照常输出，可以用变量。
// 字面量放在源码里也正好让 Tailwind 的扫描器能收录这些类名。
const SHADOWS: [string, string][] = [
  ['shadow-xs', 'xs'], ['shadow-card', 'card'], ['shadow-focus', 'focus'],
  ['shadow-lift', 'lift'], ['shadow-lift-danger', 'lift-danger'], ['shadow-modal', 'modal'],
]
const SIZES = [9, 10, 11, 12, 13, 14, 15, 16, 18, 20, 22, 24, 26]

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start gap-4 py-2 border-t border-line">
      <div className="w-[140px] flex-none text-[12px] text-faint font-mono pt-1">{label}</div>
      <div className="flex-1 min-w-0 flex flex-wrap items-center gap-2">{children}</div>
    </div>
  )
}

function Group({ title, note, children }: { title: string; note?: string; children: React.ReactNode }) {
  return (
    <section className="bg-surface border border-line2 rounded-card p-5 shadow-xs flex flex-col">
      <h3 className="m-0 text-[15px] font-bold">{title}</h3>
      {note && <p className="mt-1 mb-2 text-[12px] text-muted">{note}</p>}
      {children}
    </section>
  )
}

export function UIGallery() {
  const [on, setOn] = useState(true)
  const [menu, setMenu] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const [sel, setSel] = useState('b')

  return (
    <PageShell title="公共组件画廊" hint="ui/ 下的共用件与全部设计 token。文字说明见 ui/README.md。" width={860}>
      <Group title="颜色" note="小字一律用 amberink 而非 amber——后者在白底上约 2.3:1，不达小字对比度下限。">
        {/* 色块经 CSS 变量取值，不用 `bg-${name}` 这种运行时拼接的类名——Tailwind 只扫
            源码里的字面类名，拼出来的那个永远不会被生成。顺带这也直接证明了 token 存在
            （变量名拼错，那一格就是空的）。阴影不能这么办，原因见 SHADOWS 的注释。 */}
        <div className="grid grid-cols-4 gap-2">
          {COLORS.map(([name, zh]) => (
            <div key={name} className="flex items-center gap-2 min-w-0">
              <span className="w-7 h-7 rounded-md flex-none border border-line2" style={{ background: `var(--color-${name})` }} />
              <span className="min-w-0">
                <span className="block text-[12px] font-mono truncate">{name}</span>
                <span className="block text-[11px] text-faint truncate">{zh}</span>
              </span>
            </div>
          ))}
        </div>
      </Group>

      <Group title="圆角 / 阴影 / 字号" note="全部只用命名档：圆角 8 档、阴影 6 档、字号 13 档（无半档）。">
        <Row label="rounded-*">
          {RADII.map((r) => (
            <span
              key={r}
              className="px-3 py-2 bg-surface2 border border-line2 text-[12px] font-mono"
              // rounded-full 不是 --radius-* 里的一档（Tailwind 直接给个极大值），单列。
              style={{ borderRadius: r === 'full' ? '9999px' : `var(--radius-${r})` }}
            >{r}</span>
          ))}
        </Row>
        <Row label="shadow-*">
          {SHADOWS.map(([cls, label]) => (
            <span key={cls} className={`px-3 py-2 bg-surface rounded-btn text-[12px] font-mono ${cls}`}>{label}</span>
          ))}
        </Row>
        <Row label="text-[Npx]">
          {SIZES.map((s) => (
            <span key={s} className="font-mono text-faint" style={{ fontSize: s }}>{s}</span>
          ))}
        </Row>
      </Group>

      <Group title="反馈 · SystemNote" note="对话流里的系统事件条。刻意不是气泡：气泡在助手列里会被读成模型说的话。">
        {TONES.map((t) => (
          <SystemNote key={t} tone={t} icon={t === 'neutral' ? undefined : <WarnTriangle />} selectable>
            tone=&quot;{t}&quot; · 这条文案足够长，用来验证超出宽度时是换行居中而不是撑破面板
          </SystemNote>
        ))}
        <SystemNote sub={<span className="text-[11px] text-faint font-mono tabular-nums">↑12.3k ↓4.5k · 当前上下文 ≈48k</span>}>
          带 sub 第二行（压缩条用）
        </SystemNote>
      </Group>

      <Group title="反馈 · Banner / InlineError" note="Banner 有标题时正文用 ink，无标题时正文继承 tone 色。">
        <div className="flex flex-col gap-2">
          {TONES.map((t) => (
            <Banner key={t} tone={t} icon={<Icon name="shield" size={16} />} title={`tone="${t}" 带标题`}>
              正文用 ink，长文读起来不累。
            </Banner>
          ))}
          <Banner tone="danger" icon={<Icon name="shield" size={15} />}>
            无标题：正文<b>就是</b>这条消息，继承 tone 色而不是变灰。
          </Banner>
          <InlineError>InlineError variant=&quot;banner&quot;（默认）——要用户处理的失败</InlineError>
          <InlineError variant="text">InlineError variant=&quot;text&quot;——贴着触发它的控件</InlineError>
        </div>
      </Group>

      <Group title="布局" note="PageShell 就是本页的外框。">
        <Row label="InsetRow">
          <div className="w-full flex flex-col gap-1.5">
            <InsetRow>
              <span className="text-[13px]">已登录：<b>某某</b></span>
              <button className="text-[12px] text-muted hover:text-ink">登出</button>
            </InsetRow>
            <div className={`${INSET_BOX} text-[13px] text-muted`}>INSET_BOX：只要表面、不要 space-between</div>
          </div>
        </Row>
        <Row label="Placeholder"><div className="w-full"><Placeholder>pad=&quot;md&quot;（卡片内）</Placeholder></div></Row>
      </Group>

      <Group title="按钮 / 表单">
        <Row label="BTN">
          <button className={BTN}>普通</button>
          <button className={`${BTN} ${BTN_PRIMARY}`}>主要</button>
          <button className={`${BTN} ${BTN_DANGER}`}>危险</button>
          <button className={BTN} disabled>禁用</button>
          <GhostBtn onClick={() => {}}><Icon name="plus" size={16} />GhostBtn</GhostBtn>
        </Row>
        <Row label="FIELD_CLS">
          <input className={FIELD_CLS} placeholder="点进来看聚焦光环（shadow-focus）" style={{ width: 280 }} />
          <div style={{ width: 200 }}>
            <SelectField value={sel} onChange={setSel}>
              <option value="a">SelectField A</option>
              <option value="b">SelectField B</option>
            </SelectField>
          </div>
        </Row>
        <Row label="LABEL_CLS">
          <label className={LABEL_CLS} style={{ width: 280 }}>字段说明文字
            <input className={FIELD_CLS} placeholder="配套的竖排容器" />
          </label>
        </Row>
        <Row label="Toggle"><Toggle on={on} onChange={setOn} /><Toggle on={!on} onChange={() => setOn((v) => !v)} /><Toggle on disabled onChange={() => {}} /></Row>
      </Group>

      <Group title="浮层">
        <Row label="Popover">
          <div className="relative">
            <button className={BTN} onClick={() => setMenu((v) => !v)}>点开菜单</button>
            <Popover open={menu} onClose={() => setMenu(false)} placement="down-left" className="w-[180px]">
              {['第一项', '第二项', '第三项'].map((t) => (
                <div key={t} className="px-3 py-[7px] text-[13px] cursor-pointer text-ink hover:bg-surface2">{t}</div>
              ))}
            </Popover>
          </div>
          <button className={BTN} onClick={() => setConfirm(true)}>打开 ConfirmDialog</button>
        </Row>
      </Group>

      <Group title="预览加载态" note="来自 preview/loading。两者都有 180ms 静默期——加载够快时它们从不出现，免得闪一下比空白还烦。">
        <Row label="PreviewSkeleton"><div className="w-full max-w-[420px] border border-line2 rounded-card overflow-hidden"><PreviewSkeleton /></div></Row>
        <Row label="PreviewLoading"><PreviewLoading hint="正在用本机 Office 生成高保真预览…" /></Row>
      </Group>

      <Group title="展示件">
        <Row label="glyphs"><CheckMark size={14} className="text-green" /><WarnTriangle size={14} className="text-amberink" /><Spinner size={14} /></Row>
        <Row label="badges"><DiffStat add={12} del={3} className="font-mono text-[12px]" /><DiffStat add={5} del={0} className="font-mono text-[12px]" />{['builtin', 'user', 'project'].map((s) => <SourceBadge key={s} source={s} />)}</Row>
        <Row label="Icon">
          {['shield', 'bot', 'book', 'hash', 'paperclip', 'plus', 'send', 'stop', 'search', 'trash', 'undo', 'compass'].map((n) => (
            <span key={n} className="text-muted" title={n}><Icon name={n} size={18} /></span>
          ))}
        </Row>
        <Row label="HScroll">
          {/* 故意给一条装不下的宽度：这个件要看的就是"装不下时两端长什么样"。 */}
          <div className="w-[300px] border border-dashed border-line2 rounded-field p-2">
            <HScroll rowClassName="gap-2">
              {['录音纪要', '公文写作', '数据分析', '教学设计', '会议安排', '课程建设'].map((n) => (
                <span key={n} className="flex-none inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full border border-line2 bg-surface text-ink text-[13px] whitespace-nowrap">
                  <Icon name="sparkles" size={14} />{n}
                </span>
              ))}
            </HScroll>
          </div>
        </Row>
        <Row label="CollapsibleGroup">
          <div className="w-full">
            <CollapsibleGroup icon="file-edit" label="编辑了文件" count={4}>
              {[1, 2, 3, 4].map((i) => <div key={i} className="text-[13px] text-ink2 px-2 py-1">第 {i} 项（超过 threshold=2 默认折叠）</div>)}
            </CollapsibleGroup>
          </div>
        </Row>
      </Group>

      {confirm && (
        <ConfirmDialog
          title="确认删除？"
          message="这是应用内确认框，替代原生 window.confirm()——后者会带上 wails.localhost 的难看抬头。"
          confirmLabel="删除"
          onConfirm={() => setConfirm(false)}
          onCancel={() => setConfirm(false)}
        />
      )}
    </PageShell>
  )
}
