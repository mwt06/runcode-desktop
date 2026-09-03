// 安装技能时的等待面板。
//
// 为什么值得一块独立的面板，而不是继续用输入框上方那颗小 toast：这一步会**挡着用户
// 往下走**——提示词要等装完才填进输入框。让人盯着一颗小药丸干等十几秒，既看不出还
// 要多久，也看不出是不是已经卡死了。
//
// 进度是真的，不是按定时器推的：阶段由后端各步骤自己发，下载那一段的字节数来自
// 响应体与 Content-Length（见 internal/desktop/skillmarket.go）。假进度条在快的时候
// 显得慢、在真卡住的时候还在动，等于把唯一能判断「是不是死了」的信号也抹掉。
import { SkillInstallStages, type SkillInstallProgress } from '@/core/bridge'

/** InstallState 是界面这一侧的安装态：名字来自点击那一刻，进度来自后端事件。 */
export interface InstallState {
  name: string
  progress: SkillInstallProgress | null
}

// STAGES 是进度环下面那行字，顺序即真实顺序。done 不在里面：装完面板就撤了，
// 那一帧没人看得见，写了也是白写。
const STAGE_LABEL: Record<string, string> = {
  [SkillInstallStages.Detail]: '正在获取技能信息',
  [SkillInstallStages.Download]: '正在下载技能包',
  [SkillInstallStages.Verify]: '正在校验完整性',
  [SkillInstallStages.Extract]: '正在解压安装',
  [SkillInstallStages.Done]: '安装完成',
}

// STEPS 是底部那排步骤点，用来表达"一共几步、走到第几步了"。
const STEPS = [
  SkillInstallStages.Detail,
  SkillInstallStages.Download,
  SkillInstallStages.Verify,
  SkillInstallStages.Extract,
] as string[]

/** formatBytes 把字节数写成人读得懂的大小。导出是为了单测。 */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

/**
 * downloadPercent 算下载百分比；total 未知（服务端没给 Content-Length）时返回 null，
 * 由调用方画成不确定态。
 *
 * 夹在 0..99：**不让它到 100**。下载完还有校验和解压，进度条先到 100 再停在那儿不动，
 * 比停在 97 更像卡死了。
 */
export function downloadPercent(received: number, total: number): number | null {
  if (total <= 0) return null
  return Math.max(0, Math.min(99, Math.floor((received / total) * 100)))
}

export function InstallOverlay({ state }: { state: InstallState }) {
  const p = state.progress
  const stage = p?.stage ?? SkillInstallStages.Detail
  const pct = p?.stage === SkillInstallStages.Download ? downloadPercent(p.received, p.total) : null
  const stepIndex = STEPS.indexOf(stage)

  return (
    // 半透明遮罩 + 居中卡片，和 ConfirmDialog 同一套观感（那也是"现在只能等这件事"
    // 的场合）。这里不接 onClick 关闭：点一下并不会真的取消安装，给一个假的退出口
    // 比没有退出口更糟。
    <div className="fixed inset-0 bg-[rgba(30,33,50,0.32)] backdrop-blur-[2px] flex items-center justify-center z-30 anim-rise">
      <div className="w-[360px] max-w-[92vw] bg-surface rounded-2xl px-7 py-8 shadow-modal flex flex-col items-center">
        <Ring percent={pct} />

        <div className="mt-5 text-[15px] font-semibold text-ink text-center break-all">{state.name}</div>
        <div className="mt-1.5 text-[13px] text-muted ell">{STAGE_LABEL[stage] ?? '正在安装'}</div>

        {/* 下载阶段额外给一条字节进度。其它阶段没有可量化的量，画一条空条只是装饰。 */}
        {p?.stage === SkillInstallStages.Download && (
          <div className="w-full mt-5">
            <div className="h-1.5 rounded-full bg-surface2 overflow-hidden">
              <div
                className={`h-full bg-primary rounded-full ${pct === null ? 'w-1/3 indeterminate' : 'transition-[width] duration-200'}`}
                style={pct === null ? undefined : { width: `${pct}%` }}
              />
            </div>
            <div className="mt-1.5 flex items-center justify-between text-[12px] text-faint tabular-nums">
              <span>{formatBytes(p.received)}{p.total > 0 && ` / ${formatBytes(p.total)}`}</span>
              {pct !== null && <span>{pct}%</span>}
            </div>
          </div>
        )}

        {/* 步骤点：让人知道一共几步、还剩几步。走过的实心，当前的高亮，没到的留空。 */}
        <div className="mt-6 flex items-center gap-1.5">
          {STEPS.map((s, i) => (
            <span
              key={s}
              className={`h-1.5 rounded-full transition-all ${
                i < stepIndex ? 'w-1.5 bg-primary/45'
                  : i === stepIndex ? 'w-5 bg-primary'
                  : 'w-1.5 bg-line2'
              }`}
            />
          ))}
        </div>

        <div className="mt-5 text-[12px] text-faint text-center leading-[1.6]">
          装到「全局(用户级)」，之后每个项目都能用
        </div>
      </div>
    </div>
  )
}

// Ring 是那个进度环。用 SVG 而不是 conic-gradient：描边的圆角端点和平滑过渡都是
// 免费的，而 conic-gradient 的边是硬切的。
//
// percent 为 null 时画成一段自转的弧（不确定态）——服务端没给 Content-Length，
// 或者还没到下载那一步。
function Ring({ percent }: { percent: number | null }) {
  const R = 30
  const C = 2 * Math.PI * R
  return (
    <div className="relative w-[84px] h-[84px]">
      <svg viewBox="0 0 84 84" className={`w-full h-full -rotate-90 ${percent === null ? 'spin-ring' : ''}`}>
        <circle cx="42" cy="42" r={R} fill="none" strokeWidth="5" className="stroke-surface2" />
        <circle
          cx="42" cy="42" r={R} fill="none" strokeWidth="5" strokeLinecap="round"
          className="stroke-primary"
          strokeDasharray={C}
          // 不确定态画四分之一段弧，靠外层 spin-ring 转起来。
          strokeDashoffset={percent === null ? C * 0.75 : C * (1 - percent / 100)}
          style={percent === null ? undefined : { transition: 'stroke-dashoffset 0.2s linear' }}
        />
      </svg>
      {percent !== null && (
        <div className="absolute inset-0 flex items-center justify-center text-[17px] font-semibold text-ink tabular-nums">
          {percent}<span className="text-[11px] text-faint ml-0.5">%</span>
        </div>
      )}
    </div>
  )
}
