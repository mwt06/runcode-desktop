// 关于与更新：当前版本、检查更新，以及发现新版之后的下载与安装。
//
// 整节完全按 update.stage 分支渲染，一个阶段画一块，不做任何本地推导——状态由 Go
// 侧那台状态机说了算（见 session/use-update.ts 的说明）。stage 在 TS 里是封闭联合，
// 少画一个分支 tsc 会当场报错，所以「新加了个阶段却忘了画」不可能悄悄溜过去。
import { type ReactElement } from 'react'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { Icon } from '@/ui/icons'
import { Banner, InlineError } from '@/ui/feedback'
import { INSET_BOX, InsetRow } from '@/ui/layout'
import { BRAND } from '@/core/brand'
import { fmtBytes } from '@/core/format'
import { UpdateStages } from '@/core/bridge'
import { type UpdateController } from '@/session/use-update'
import { Section } from './section'

export function AboutSection({ update }: { update: UpdateController }) {
  const { info, stage, busy } = update
  return (
    <Section title="关于与更新" hint={checkedHint(info?.checkedAt)}>
      <InsetRow>
        <span className="min-w-0">
          <span className="text-[13px] text-ink">{BRAND.name}</span>
          <span className="block text-[12px] text-muted mt-0.5">
            当前版本 <span className="font-mono">{info?.current || '—'}</span>
          </span>
        </span>
        <button
          type="button"
          className={`${BTN} px-4 flex-none inline-flex items-center gap-1.5`}
          onClick={update.check}
          disabled={busy}
        >
          <Icon name="refresh" size={13} />
          {stage === UpdateStages.Checking ? '检查中…' : '检查更新'}
        </button>
      </InsetRow>

      {/* 「上次没装上」独立于本轮状态：它是启动时算出来的历史结论，不属于 stage
          里的任何一格，所以摆在 UpdateBody 之外，与它同时显示。 */}
      {info?.installError && (
        <Banner tone="warning" title="上次更新未完成">
          {info.installError}
        </Banner>
      )}

      <UpdateBody update={update} />
    </Section>
  )
}

// UpdateBody 是版本状态那一块。
//
// 分成独立组件、并且**显式写出返回类型**，都是为了同一件事：让下面这个 switch 必须
// 穷尽覆盖 UpdateStage 的每个取值。不写返回类型的话，漏一个分支只会让推导出来的
// 返回类型多一个 undefined——对组件来说完全合法，于是新加的阶段会静静地渲染成一片
// 空白。写上之后漏一个分支就是「函数缺少结尾的 return 语句」，当场编译不过。
function UpdateBody({ update }: { update: UpdateController }): ReactElement | null {
  const { info, stage } = update
  // 通道异常（命令都没打出去）与业务失败（网关 500、校验不过）不会同时发生，
  // 取其一即可；后者由后端写进 info.error 并随状态推过来。
  const failure = update.error || info?.error || ''

  switch (stage) {
    case UpdateStages.Idle:
      // 还没查过。不画任何东西——启动几秒后的自动检查会把这里填上，而在那之前
      // 摆一句「未检查」只是在让人担心。
      return null

    case UpdateStages.Checking:
      return <p className="text-[12px] text-muted">正在检查更新…</p>

    case UpdateStages.Latest:
      return (
        <p className="text-[12px] text-muted inline-flex items-center gap-1.5">
          <Icon name="shield" size={13} className="text-green" />
          已是最新版本
        </p>
      )

    case UpdateStages.Available:
      return (
        <div className={`${INSET_BOX} flex flex-col gap-2.5`}>
          <NewVersionHead info={info} />
          <div className="flex items-center gap-2">
            <button type="button" className={`${BTN} ${BTN_PRIMARY} px-4`} onClick={update.download}>
              下载{info?.size ? `（${fmtBytes(info.size)}）` : ''}
            </button>
            <span className="text-[12px] text-faint">下载完成后再决定什么时候安装</span>
          </div>
        </div>
      )

    case UpdateStages.Downloading:
      return (
        <div className={`${INSET_BOX} flex flex-col gap-2.5`}>
          <NewVersionHead info={info} />
          <Progress received={info?.received ?? 0} total={info?.size ?? 0} />
          <div className="flex items-center gap-2">
            <button type="button" className={`${BTN} px-4`} onClick={update.cancel}>
              取消下载
            </button>
            <span className="text-[12px] text-faint">
              {info?.size
                ? `${fmtBytes(info.received)} / ${fmtBytes(info.size)}`
                : `已下载 ${fmtBytes(info?.received) || '0 B'}`}
            </span>
          </div>
        </div>
      )

    case UpdateStages.Verifying:
      return (
        <div className={`${INSET_BOX} flex flex-col gap-2.5`}>
          <NewVersionHead info={info} />
          {/* 校验大包要几秒，进度条会停在 100% 不动——这句话是它在这几秒里唯一
              能说明自己没死的方式。 */}
          <p className="text-[12px] text-muted">正在校验安装包…</p>
        </div>
      )

    case UpdateStages.Ready:
      return (
        <div className={`${INSET_BOX} flex flex-col gap-2.5`}>
          <NewVersionHead info={info} />
          <div className="flex items-center gap-2 flex-wrap">
            <button type="button" className={`${BTN} ${BTN_PRIMARY} px-4`} onClick={update.install}>
              {info?.canInstall ? '立即安装并重启' : '打开安装包所在文件夹'}
            </button>
            <span className="text-[12px] text-faint">
              {/* 三种结局各说各的，别许一个做不到的承诺：静默安装会自己回来；
                  走向导的（认不出安装目录，基本只有开发构建）要用户自己点图标；
                  macOS 根本不由应用接管安装。 */}
              {!info?.canInstall
                ? '把新版本拖进「应用程序」覆盖旧版即可'
                : info.autoRestart
                  ? '将关闭本应用并自动完成安装，装好后会自动重新打开（需授权一次）'
                  : '将关闭本应用并运行安装程序，安装完成后重新打开即可'}
            </span>
          </div>
          {!info?.canInstall && info?.file && (
            <p className="text-[12px] text-faint font-mono break-all">{info.file}</p>
          )}
        </div>
      )

    case UpdateStages.Failed:
      return (
        <div className="flex flex-col gap-2.5">
          <InlineError variant="banner">{failure || '检查更新失败'}</InlineError>
          <div>
            <button type="button" className={`${BTN} px-4`} onClick={update.check}>
              重试
            </button>
          </div>
        </div>
      )
  }
}

// NewVersionHead 是「新版本 x.y.z」那一行加更新说明，四个阶段共用。
function NewVersionHead({ info }: { info: UpdateController['info'] }) {
  if (!info) return null
  return (
    <div>
      <div className="text-[13px] text-ink">
        新版本 <span className="font-mono">{info.latest}</span>
        {info.publishedAt && <span className="text-[12px] text-faint ml-2">{fmtDate(info.publishedAt)}</span>}
      </div>
      {info.notes && (
        // 更新说明按原样保留换行：发布方写的是一条条改动，挤成一段就没法扫了。
        <p className="text-[12px] text-muted mt-1.5 whitespace-pre-wrap">{info.notes}</p>
      )}
    </div>
  )
}

// Progress 是下载进度条。总长未知时画成不确定态——服务端没给 Content-Length 时
// 硬算一个百分比只会得到 Infinity 或一根永远不动的空条。
function Progress({ received, total }: { received: number; total: number }) {
  const pct = total > 0 ? Math.min(100, Math.round((received / total) * 100)) : null
  return (
    <div className="h-1.5 rounded-full bg-line2 overflow-hidden">
      <div
        className={`h-full bg-primary transition-[width] ${pct === null ? 'animate-pulse w-1/3' : ''}`}
        style={pct === null ? undefined : { width: `${pct}%` }}
      />
    </div>
  )
}

// checkedHint 是小节标题右侧那句「刚刚检查过」。没查过就不显示。
function checkedHint(checkedAt?: string): string | undefined {
  if (!checkedAt) return undefined
  const at = new Date(checkedAt)
  if (Number.isNaN(at.getTime())) return undefined
  const mins = Math.floor((Date.now() - at.getTime()) / 60000)
  if (mins < 1) return '刚刚检查过'
  if (mins < 60) return `${mins} 分钟前检查过`
  return `检查于 ${fmtDate(checkedAt)}`
}

// fmtDate 把 RFC3339 渲染成本地日期。解不出来就原样显示——服务端给了个我们不认识
// 的格式时，把原文摆出来比显示 "Invalid Date" 有用得多。
function fmtDate(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleDateString()
}
