// PermissionsPage: pick a mode (the hero), then scan the operation × mode matrix for
// exactly what each mode does. Clicking a mode switches the live session to it.

// PERM_CHIP maps a permission outcome to its label + color. 'auto' is an allow the
// smart mode grants without asking (workspace mutations); it reads green with an
// "自动" tag so it's distinct from a plain allow.
const PERM_CHIP: Record<string, { label: string; cls: string; auto?: boolean }> = {
  allow: { label: '允许', cls: 'text-green bg-green/12' },
  ask: { label: '询问', cls: 'text-amber bg-amber/15' },
  judge: { label: '审查', cls: 'text-primary bg-primary/12' },
  deny: { label: '拒绝', cls: 'text-red bg-red/12' },
  auto: { label: '允许', cls: 'text-green bg-green/12', auto: true },
}

function PermChip({ kind }: { kind: string }) {
  const c = PERM_CHIP[kind] ?? PERM_CHIP.deny
  return (
    <span className={`inline-flex items-center justify-center gap-1 text-[12.5px] font-medium px-3 py-[5px] rounded-full whitespace-nowrap ${c.cls}`}>
      {c.label}
      {c.auto && <span className="font-mono text-[9px] tracking-wide opacity-70">自动</span>}
    </span>
  )
}

// level is how much a mode lets through (1 = most guarded … 4 = fully open); tone is
// its risk color. Together they read as a spectrum from safe/green to flight/red.
const PERM_MODES = [
  { key: 'safe', zh: '安全', en: 'safe', level: 1, tone: 'bg-green', essence: '只放行绝对安全的,其余一律拒绝,从不打扰你。' },
  { key: 'interactive', zh: '交互', en: 'interactive', level: 2, tone: 'bg-primary', essence: '项目内读取自动放行,写改 / 命令 / 联网 / 出项目逐项当面问你。' },
  { key: 'judge', zh: '智能', en: 'judge', level: 3, tone: 'bg-amber', essence: '工作区改动直接放行,命令 / 联网 / 项目外读取交模型先审,项目外落盘仍要你点头。' },
  { key: 'flight', zh: '飞行', en: 'flight', level: 4, tone: 'bg-red', essence: '全部放行,连高危命令也不拦,不审计。' },
]

// PERM_ROWS is the operation × mode matrix, verified against internal/permissions.
// cells are ordered [安全, 交互, 智能, 飞行].
const PERM_ROWS: { op: string; q: string; cells: string[] }[] = [
  { op: '读取', q: 'Read · Grep · Glob', cells: ['allow', 'allow', 'allow', 'allow'] },
  { op: '待办 / 记忆', q: 'TodoWrite · Memory', cells: ['allow', 'allow', 'allow', 'allow'] },
  { op: '写 / 改 · 已读文件', q: 'Write · Edit', cells: ['deny', 'ask', 'auto', 'allow'] },
  { op: '写 / 改 · 没读就覆盖', q: '读态拦截 · 可恢复', cells: ['deny', 'deny', 'auto', 'allow'] },
  { op: '删除 · 工作区内', q: 'Delete', cells: ['deny', 'ask', 'auto', 'allow'] },
  { op: '命令', q: 'Bash', cells: ['deny', 'ask', 'judge', 'allow'] },
  { op: '联网', q: 'WebFetch · WebSearch', cells: ['deny', 'ask', 'judge', 'allow'] },
  { op: 'MCP 外部工具', q: '独立进程', cells: ['deny', 'ask', 'judge', 'allow'] },
  { op: '越出工作区 · 读取', q: '项目外的文件 / 目录 / shell 读', cells: ['deny', 'ask', 'judge', 'allow'] },
  { op: '越出工作区 · 写改删', q: '项目外落盘，智能模式也必问', cells: ['deny', 'ask', 'ask', 'allow'] },
  { op: '高危命令', q: '提权 · rm -rf · 毁灭性 git', cells: ['deny', 'deny', 'deny', 'allow'] },
]

const PERM_CHOICES: [string, string, boolean][] = [
  ['本次会话', '推荐。本会话内同类命令、本项目文件改动都不再问。', false],
  ['仅此一次', '只放行这一次,下次遇到同样的动作还会再问。', false],
  ['本项目', '对这个项目永久记住这条放行。', false],
  ['拒绝', '拒绝并停止本回合,把控制权交回给你,模型不会重试。', true],
]

const PERM_RULES: [string, string, string][] = [
  ['计划模式', 'text-primaryink bg-primarysoft', '规划阶段任何会改动东西的动作都被拒绝,让 AI 先想清楚——与四种模式叠加。'],
  ['危害审查', 'text-amber bg-amber/15', '智能模式对命令 / 联网 / MCP 先判危害:安全放行,有害弹窗并附原因,评估失败也弹窗,绝不静默放行。'],
  ['并发队列', 'text-green bg-greenbg', '只读类工具并行执行;多个授权请求排队逐个弹出、互不覆盖;同一批里问题相同的请求只弹一次、共用答复;批次里任一被拒绝同样停止本回合。'],
  ['越界授权按目录记', 'text-amber bg-amber/15', '批准一个项目外的文件 = 批准它所在目录及其子目录;读与写分开记,写授权蕴含读,读授权不含写。项目内那条"文件改动"授权永远不会外溢到项目之外。'],
]

// AutonomyMeter renders a mode's "how much it lets through" as four segments filled
// to its level — the page's one signature element, and true to the content.
function AutonomyMeter({ level, tone }: { level: number; tone: string }) {
  return (
    <div className="flex gap-1" title={`自主度 ${level} / 4`} aria-label={`自主度 ${level} / 4`}>
      {[1, 2, 3, 4].map((i) => (
        <span key={i} className={`w-4 h-1.5 rounded-full ${i <= level ? tone : 'bg-line2'}`} />
      ))}
    </div>
  )
}

export function PermissionsPage({ mode, onPickMode }: { mode?: string; onPickMode: (m: string) => void }) {
  return (
    <div className="flex-1 overflow-y-auto px-[26px] py-9">
      <div className="max-w-[920px] mx-auto flex flex-col gap-9">
        <div>
          <h2 className="m-0 text-[24px] font-bold tracking-tight">权限</h2>
          <p className="mt-3 text-[15px] text-muted leading-[1.8] max-w-[60ch]">
            每次 AI 调用工具都走同一套判定:<b className="text-ink font-semibold">策略先看动作本身</b>,给出「允许 / 询问 / 拒绝」;再由你选的<b className="text-ink font-semibold">模式决定「询问」如何落地</b>。所以读工作区文件永远放行、提权命令永远拒绝——真正被模式改写的,只是那些需要询问的动作。
          </p>
        </div>

        {/* Modes — the hero. The autonomy meter (1–4 bars) reads left→right as a
            spectrum from most guarded (安全) to fully open (飞行). */}
        <section className="flex flex-col gap-4">
          <div className="flex items-baseline gap-3">
            <span className="text-[15px] font-semibold text-ink">选择模式</span>
            <span className="text-[13px] text-faint">点一下即切换当前会话</span>
          </div>
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-3.5">
            {PERM_MODES.map((m) => {
              const active = mode === m.key
              return (
                <button
                  key={m.key}
                  onClick={() => onPickMode(m.key)}
                  className={`text-left rounded-[16px] border p-[22px] bg-surface transition-all duration-150 ${active ? 'border-primary shadow-[0_0_0_3px_var(--color-primarysoft)]' : 'border-line2 hover:shadow-[0_5px_18px_rgba(30,35,60,0.08)] hover:-translate-y-0.5'}`}
                >
                  <div className="flex items-center justify-between h-6 mb-4">
                    <AutonomyMeter level={m.level} tone={m.tone} />
                    {active && <span className="text-[11px] font-semibold text-white bg-primary rounded-full px-2.5 py-1 leading-none tracking-wide">当前</span>}
                  </div>
                  <div className="flex items-baseline gap-2">
                    <span className="text-[19px] font-bold tracking-tight text-ink">{m.zh}</span>
                    <span className="font-mono text-[11.5px] text-faint">{m.en}</span>
                  </div>
                  <div className="text-[14px] text-muted mt-2.5 leading-[1.7]">{m.essence}</div>
                </button>
              )
            })}
          </div>
        </section>

        {/* Matrix — the reference. The chosen mode's column is tinted so its column
            reads at a glance. */}
        <section className="flex flex-col gap-4">
          <div className="flex items-baseline justify-between flex-wrap gap-y-2 gap-x-4">
            <span className="text-[15px] font-semibold text-ink">各模式如何处理每类操作</span>
            <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-[12.5px] text-muted">
              <span className="inline-flex items-center gap-1.5"><i className="w-2.5 h-2.5 rounded-full bg-green" />允许</span>
              <span className="inline-flex items-center gap-1.5"><i className="w-2.5 h-2.5 rounded-full bg-amber" />询问</span>
              <span className="inline-flex items-center gap-1.5"><i className="w-2.5 h-2.5 rounded-full bg-primary" />智能审查</span>
              <span className="inline-flex items-center gap-1.5"><i className="w-2.5 h-2.5 rounded-full bg-red" />拒绝</span>
            </div>
          </div>
          <div className="overflow-x-auto rounded-[16px] border border-line2 bg-surface">
            <table className="w-full border-collapse text-[14px] min-w-[620px]">
              <thead>
                <tr>
                  <th className="text-left font-medium text-[13px] text-faint px-6 py-4">操作</th>
                  {PERM_MODES.map((m) => {
                    const on = m.key === mode
                    return (
                      <th key={m.key} className={`px-3 py-4 text-center align-bottom ${on ? 'bg-primarysoft/50' : ''}`}>
                        <div className={`text-[14.5px] font-semibold ${on ? 'text-primaryink' : 'text-ink'}`}>{m.zh}</div>
                        <div className="h-[15px] mt-0.5">{on && <span className="text-[10px] font-mono text-primary">当前</span>}</div>
                      </th>
                    )
                  })}
                </tr>
              </thead>
              <tbody>
                {PERM_ROWS.map((r) => (
                  <tr key={r.op} className="border-t border-line">
                    <td className="px-6 py-4">
                      <div className="text-ink text-[14px]">{r.op}</div>
                      <div className="font-mono text-[11.5px] text-faint mt-1">{r.q}</div>
                    </td>
                    {r.cells.map((c, ci) => (
                      <td key={ci} className={`px-3 py-4 text-center ${PERM_MODES[ci].key === mode ? 'bg-primarysoft/40' : ''}`}>
                        <PermChip kind={c} />
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        {/* Footer — quiet: what a prompt offers, plus the cross-cutting rules. */}
        <section className="grid grid-cols-1 md:grid-cols-2 gap-3.5">
          <div className="rounded-[16px] border border-line2 bg-surface p-6">
            <div className="text-[15px] font-semibold text-ink mb-4">被询问时,你能选</div>
            <div className="flex flex-col gap-3">
              {PERM_CHOICES.map(([k, d, stop]) => (
                <div key={k} className="flex gap-3 items-start">
                  <span className={`font-mono text-[12.5px] font-medium rounded-[7px] px-2.5 py-1 min-w-[74px] text-center flex-none border ${stop ? 'text-red border-red/35 bg-red/[0.06]' : 'text-ink bg-surface2 border-line2'}`}>{k}</span>
                  <span className="text-[14px] text-muted leading-[1.7]">{d}</span>
                </div>
              ))}
            </div>
          </div>
          <div className="rounded-[16px] border border-line2 bg-surface p-6">
            <div className="text-[15px] font-semibold text-ink mb-4">还有几条连带规则</div>
            <div className="flex flex-col gap-3.5">
              {PERM_RULES.map(([b, cls, d]) => (
                <div key={b} className="flex gap-3 items-start">
                  <span className={`font-mono text-[12px] font-medium rounded-[6px] px-2 py-1 flex-none mt-0.5 whitespace-nowrap ${cls}`}>{b}</span>
                  <span className="text-[14px] text-muted leading-[1.7]">{d}</span>
                </div>
              ))}
            </div>
          </div>
        </section>
      </div>
    </div>
  )
}
