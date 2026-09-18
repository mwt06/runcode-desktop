// 运行时环境：Python / Node.js / Git 的安装与状态。
//
// 每一行完全由后端那份 RuntimePack 决定，前端不做推导（见 session/use-runtimes.ts）。
// 一行要同时讲清楚两件**正交**的事，这是本节所有排版的由来：
//
//   - 托管包（应用自带的那份）装没装、装到哪一步；
//   - 系统里本来有没有、够不够新。
//
// 两件事不能揉成一个状态：银河麒麟 V10 上「系统有 Python 3.8」和「够不上技能要的
// 3.11」同时成立，只画一个绿勾的话，用户会看着"已安装"却跑不起任何一个办公技能。
import { type ReactElement } from 'react'
import { BTN, BTN_DANGER, BTN_PRIMARY } from '@/ui/tokens'
import { Icon } from '@/ui/icons'
import { Banner, InlineError } from '@/ui/feedback'
import { InsetRow } from '@/ui/layout'
import { fmtBytes } from '@/core/format'
import { RuntimeStages, type RuntimePack } from '@/core/bridge'
import { packBusy, type RuntimeController } from '@/session/use-runtimes'
import { Section } from './section'

export function RuntimesSection({ runtimes }: { runtimes: RuntimeController }) {
  const { info, packs, checking } = runtimes
  return (
    <Section title="运行时环境" hint={info?.platform}>
      <div className="text-[12px] text-muted">
        办公技能（公文、会议纪要、表格）要用 Python 跑。装在本应用自己的目录里，
        不会影响系统里已有的版本。银河麒麟开着安全中心的执行控制时，装完需要输一次本机密码，
        让安全中心放行它。
      </div>

      {runtimes.error && <InlineError>{runtimes.error}</InlineError>}
      {info?.error && (
        <Banner tone="warning" title="获取运行时清单失败">
          {info.error}
          <span className="block mt-1 text-[12px]">已装好的运行时不受影响，可以继续使用。</span>
        </Banner>
      )}

      {packs.map((p) => (
        <PackRow key={p.id} pack={p} runtimes={runtimes} />
      ))}

      <InsetRow>
        <span className="text-[12px] text-muted min-w-0">
          {info?.fetched ? '清单已获取' : '正在获取可安装的运行时…'}
        </span>
        <button
          type="button"
          className={`${BTN} px-4 flex-none inline-flex items-center gap-1.5`}
          onClick={runtimes.check}
          disabled={checking}
        >
          <Icon name="refresh" size={13} />
          {checking ? '检查中…' : '重新检查'}
        </button>
      </InsetRow>
    </Section>
  )
}

function PackRow({ pack, runtimes }: { pack: RuntimePack; runtimes: RuntimeController }) {
  return (
    <InsetRow>
      <span className="min-w-0">
        <span className="text-[13px] text-ink">{pack.label}</span>
        <span className="block text-[12px] text-muted mt-0.5">{pack.summary}</span>
        <PackStatus pack={pack} />
      </span>
      <PackAction pack={pack} runtimes={runtimes} />
    </InsetRow>
  )
}

// PackStatus 是那一行的第二、第三句：托管包的状态，以及系统里那一份。
function PackStatus({ pack }: { pack: RuntimePack }): ReactElement {
  return (
    <span className="block text-[12px] mt-1">
      <span className={pack.stage === RuntimeStages.Ready ? 'text-primary' : 'text-faint'}>
        {managedText(pack)}
      </span>
      {systemText(pack) && <span className="block text-faint mt-0.5">{systemText(pack)}</span>}
      {pack.needsAuthorize && (
        <span className="block mt-1 text-amberink">
          麒麟安全中心还没放行它：每次运行都会在桌面弹安全框，30 秒没人点就失败。点右边「授权」输一次密码即可。
        </span>
      )}
      {pack.stage === RuntimeStages.Failed && pack.error && (
        <span className="block mt-1">
          <InlineError variant="text">{pack.error}</InlineError>
        </span>
      )}
    </span>
  )
}

// managedText 讲托管包这一半。
//
// 显式写出返回类型，好让下面的 switch 必须穷尽 RuntimeStage 的每个取值——漏一个
// 分支会让推导出来的类型多一个 undefined（对 React 完全合法），于是新加的阶段会
// 静静地渲染成一片空白。写上之后漏一个就是编译错误。
function managedText(pack: RuntimePack): string {
  switch (pack.stage) {
    case RuntimeStages.Ready:
      return `应用自带 ${pack.version}` + (pack.available && pack.available !== pack.version ? `（可更新到 ${pack.available}）` : '')
    case RuntimeStages.Downloading:
      return pack.size > 0
        ? `正在下载… ${fmtBytes(pack.received)} / ${fmtBytes(pack.size)}`
        : `正在下载… ${fmtBytes(pack.received)}`
    case RuntimeStages.Verifying:
      return '正在校验完整性…'
    case RuntimeStages.Extracting:
      return '正在解压…（文件较多，请稍候）'
    case RuntimeStages.Authorizing:
      return '正在请麒麟安全中心放行…（请在弹出的框里输入本机密码）'
    case RuntimeStages.Failed:
      return '安装失败'
    case RuntimeStages.Absent:
      if (pack.available) {
        return `可安装 ${pack.available}${pack.size > 0 ? `（${fmtBytes(pack.size)}）` : ''}`
      }
      // 这个平台没发这个包（Git 在 Linux/macOS 上就是如此）。
      return '本平台暂未提供安装包'
  }
}

// systemText 讲系统自带的那一半——只在它能改变用户判断时才出现。
function systemText(pack: RuntimePack): string {
  if (!pack.systemVersion) return ''
  if (pack.systemUsable) {
    return pack.stage === RuntimeStages.Ready
      ? `系统里另有 ${pack.systemVersion}（未使用）`
      : `使用系统自带的 ${pack.systemVersion}`
  }
  // 「有，但太旧」这句必须说出来。麒麟 V10 正是这一格：看着有 Python，技能却全跑不了。
  return `系统自带的是 ${pack.systemVersion}，低于所需的 ${pack.minVersion}`
}

function PackAction({ pack, runtimes }: { pack: RuntimePack; runtimes: RuntimeController }): ReactElement | null {
  if (packBusy(pack)) {
    return (
      <button type="button" className={`${BTN} px-4 flex-none`} onClick={() => runtimes.cancel(pack.id)}>
        取消
      </button>
    )
  }
  if (pack.stage === RuntimeStages.Ready) {
    const outdated = pack.available && pack.available !== pack.version
    return (
      <span className="flex-none flex items-center gap-2">
        {pack.needsAuthorize && (
          <button type="button" className={`${BTN} ${BTN_PRIMARY} px-4`} onClick={() => runtimes.authorize(pack.id)}>
            授权
          </button>
        )}
        {outdated && (
          <button type="button" className={`${BTN} ${BTN_PRIMARY} px-4`} onClick={() => runtimes.install(pack.id)}>
            更新
          </button>
        )}
        <button type="button" className={`${BTN} ${BTN_DANGER} px-3`} onClick={() => runtimes.remove(pack.id)}>
          <Icon name="trash" size={13} />
        </button>
      </span>
    )
  }
  if (!pack.available) return null
  return (
    <button
      type="button"
      className={`${BTN} ${BTN_PRIMARY} px-4 flex-none`}
      onClick={() => runtimes.install(pack.id)}
    >
      {pack.stage === RuntimeStages.Failed ? '重试' : '安装'}
    </button>
  )
}
