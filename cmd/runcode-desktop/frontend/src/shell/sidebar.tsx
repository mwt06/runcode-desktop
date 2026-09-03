import { useState } from 'react'
import { Icon } from '@/ui/icons'
import { ConfirmDialog } from '@/ui/confirm-dialog'
import { Popover } from '@/ui/popover'
import { basename } from '@/core/paths'
import { type SessionSummary } from '@/core/bridge'

// Sidebar 是主界面左栏：新建对话、页面导航、最近对话列表与工作区切换器。
// 全部数据与动作经 props 注入（自身只持有弹层/确认框的局部状态），与 App 的
// 会话状态解耦——从 App.tsx 原样搬出，行为不变。
// OpenSessionRow 是「打开中」那一栏的一行。数据来自三处：后端的 OpenSessions
// （是谁、在哪）、对话状态（有没有回合在跑）、授权队列（有几个在等）。
export interface OpenSessionRow {
  id: string
  title: string
  running: boolean
  /** waiting 是这条会话有几个授权请求在等人应答。 */
  waiting: number
  focused: boolean
  /** workspace 是这条会话的目录。几条会话落在**不同目录**时才显示出来。 */
  workspace: string
}

// PROFILE_NAV 是收在底部用户区菜单里的几个入口：配置类的，配好之后很少再动。
//
// 与顶部那三个（对话/插件/市场）分开，是因为它们的使用频次差一个数量级——六个入口
// 并排会让每天要点很多次的那几个和一周点不了一次的挤在一起抢注意力。
const PROFILE_NAV = [
  { label: '记忆', name: 'file', view: 'memory' as const },
  { label: '权限', name: 'shield', view: 'permissions' as const },
  { label: '设置', name: 'settings', view: 'settings' as const },
]

export function Sidebar({
  collapsed,
  hasUpdate,
  recents,
  openSessions,
  onFocusSession,
  onCloseSession,
  currentId,
  cwd,
  recentWorkspaces,
  onPickWorkspace,
  onSwitchWorkspace,
  onDelete,
  view,
  onNav,
  onNew,
  onResume,
  userName,
  avatar,
  onLogout,
}: {
  collapsed: boolean
  recents: SessionSummary[]
  openSessions: OpenSessionRow[]
  onFocusSession: (id: string) => void
  onCloseSession: (id: string) => void
  currentId?: string
  cwd?: string
  recentWorkspaces: string[]
  onPickWorkspace: (path: string) => void
  onSwitchWorkspace: () => void
  onDelete: (id: string) => void
  view: 'chat' | 'settings' | 'plugins' | 'market' | 'permissions' | 'memory'
  onNav: (v: 'chat' | 'settings' | 'plugins' | 'market' | 'permissions' | 'memory') => void
  onNew: () => void
  onResume: (id: string) => void
  // 已登录用户的展示名与头像;未登录为空——此时用户区照样渲染(里面装着设置/权限/
  // 记忆),只是不显示头像与「退出登录」。
  userName?: string
  avatar?: string
  // hasUpdate 为真时用户区点一颗小红点。**只点一个点、不弹窗**：更新不紧急，
  // 而弹窗会打断用户手头正在做的事；红点一直在，等他自己走到设置页。
  hasUpdate?: boolean
  onLogout: () => void
}) {
  // Workspace switcher popover: search + recent-workspace list + browse.
  const [wsOpen, setWsOpen] = useState(false)
  const [wsQuery, setWsQuery] = useState('')
  // 用户区菜单(记忆/权限/设置 + 退出登录)的开合。
  const [profileOpen, setProfileOpen] = useState(false)
  // 待确认删除的会话（打开自定义确认弹窗，替代原生 window.confirm）。
  const [confirmDel, setConfirmDel] = useState<SessionSummary | null>(null)
  // 待确认关闭的会话——只有回合正在跑的那些会走到这里。
  const [confirmClose, setConfirmClose] = useState<OpenSessionRow | null>(null)
  const wsq = wsQuery.trim().toLowerCase()
  const wsMatches = recentWorkspaces.filter((w) => w && w !== cwd && (!wsq || w.toLowerCase().includes(wsq)))
  // 折叠成图标栏（而非整块隐藏）：导航/新建/工作区仍可点，只收起文字与最近对话。
  // collapsed 由父组件（App）提供——折叠开关已移到主栏顶部状态条（「空闲」前）。
  const wsName = cwd ? cwd.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || cwd : '—'
  // 开着的几条会话是否落在不止一个目录里——决定「打开中」那一栏要不要标出目录名。
  const crossWorkspace = new Set(openSessions.map((s) => s.workspace)).size > 1
  // 顶部导航是「常去的地方」：每天要点很多次的那三个。
  //
  // 记忆/权限/设置挪到了底部用户区的菜单里（PROFILE_NAV）——它们是配置类的入口，
  // 一次配好之后很少再动，和对话/插件/市场并排等于让六个入口抢同一片注意力，
  // 而其中三个一周也点不了一次。
  const nav = [
    { label: '对话', name: 'chat', view: 'chat' as const },
    { label: '插件', name: 'grid', view: 'plugins' as const },
    { label: '市场', name: 'compass', view: 'market' as const },
  ]
  // 当前视图落在菜单里的某一项时，底部那个入口要高亮：否则打开设置页之后侧栏
  // 一处高亮都没有，人会不知道自己在哪。
  const profileActive = PROFILE_NAV.some((n) => n.view === view)
  return (
    <aside
      className={`${collapsed ? 'w-[64px] px-2.5' : 'w-[268px] px-4'} py-4 flex-none bg-surface border-r border-line2 flex flex-col transition-[width] duration-200 ease-out`}
    >
      <button
        className={`w-full border-none bg-primary text-white font-semibold text-sm rounded-btn cursor-pointer inline-flex items-center justify-center gap-2 shadow-lift hover:brightness-105 transition ${collapsed ? 'h-10' : 'py-3'}`}
        onClick={onNew}
        title={collapsed ? '新建对话' : undefined}
      >
        <Icon name="plus" size={16} />
        {!collapsed && '新建对话'}
      </button>
      <nav className="mt-[18px] flex flex-col gap-0.5">
        {nav.map((n) => (
          <div
            key={n.label}
            onClick={() => onNav(n.view)}
            title={collapsed ? n.label : undefined}
            className={`flex items-center py-[9px] rounded-field text-sm cursor-pointer select-none ${
              collapsed ? 'justify-center' : 'gap-[11px] px-[11px]'
            } ${view === n.view ? 'bg-primarysoft text-primaryink font-semibold' : 'text-muted hover:bg-surface2 hover:text-ink'}`}
          >
            <Icon name={n.name} size={18} />
            {!collapsed && n.label}
          </div>
        ))}
      </nav>
      {collapsed ? (
        // 撑开中间，让工作区按钮保持贴底（展开态由最近对话列表的 flex-1 承担）。
        <div className="flex-1" />
      ) : (
        <div className="mt-[22px] flex-1 overflow-y-auto -mr-1 pr-1">
          {openSessions.length > 0 && (
            <>
              <div className="text-[12px] text-faint px-[11px] pb-2 tracking-wide">打开中</div>
              {openSessions.map((s) => (
                <div
                  key={s.id}
                  onClick={() => onFocusSession(s.id)}
                  title={s.title}
                  className={`group flex items-center gap-2.5 px-[11px] py-[9px] rounded-field cursor-pointer text-[14px] mb-0.5 ${
                    s.focused ? 'text-ink bg-surface2' : 'text-muted hover:bg-surface2'
                  }`}
                >
                  {/* 运行指示：跑着时是一个呼吸的点（blip 是既有的那个动画，思考指示器也用它），
                      闲着是静态小圆。 */}
                  <span
                    className={`flex-none w-[7px] h-[7px] rounded-full ${
                      s.running ? 'bg-primary blip' : 'bg-line2'
                    }`}
                    aria-hidden
                  />
                  <span className="flex-1 min-w-0 truncate">{s.title}</span>
                  {/* 目录名。只有几条会话**落在不同目录**时才出现——同一个目录里全都
                      一样，那就是纯噪声。多工作区并行时它是唯一能区分"两条同名会话
                      分别属于哪个项目"的东西。 */}
                  {crossWorkspace && s.workspace && (
                    <span
                      title={s.workspace}
                      className="flex-none max-w-[76px] truncate text-[11px] text-faint font-mono"
                    >
                      {basename(s.workspace)}
                    </span>
                  )}
                  {/* 待审批角标。后台会话卡在等授权而界面毫无表示，是并行里最坏的
                      一种失败——任务停着，用户以为它在跑。所以它**不随 hover 隐藏**，
                      也不被关闭按钮顶掉。 */}
                  {s.waiting > 0 && (
                    <span
                      title={`有 ${s.waiting} 个操作在等你确认`}
                      className="flex-none min-w-[17px] h-[17px] px-1 rounded-full bg-amber text-white text-[11px] leading-[17px] text-center tabular-nums"
                    >
                      {s.waiting}
                    </span>
                  )}
                  {/* 关闭按钮**常驻**，不随 hover 出现。
                      悬停才显形的按钮会在指针移到行上的那一刻**凭空出现在指针底下**，
                      于是"想切过去"点成了"关掉"——一次点击销毁一条正在跑的会话，
                      而这一栏里的行大多正是并行跑着的活。常驻的代价是行里多一个字符，
                      换来的是这个按钮只会被瞄准了才点到。 */}
                  <button
                    type="button"
                    title="关闭这条会话"
                    onClick={(e) => {
                      e.stopPropagation()
                      // 跑着的活多问一句。关闭会当场取消回合，而且撤不回来。
                      if (s.running) setConfirmClose(s)
                      else onCloseSession(s.id)
                    }}
                    className="flex-none text-faint/70 hover:text-red leading-none px-0.5"
                  >
                    ✕
                  </button>
                </div>
              ))}
              <div className="h-[14px]" />
            </>
          )}
          <div className="text-[12px] text-faint px-[11px] pb-2 tracking-wide">最近对话</div>
          {recents.length === 0 ? (
            <div className="text-faint text-[13px] py-1 text-center">暂无对话</div>
          ) : (
            recents.map((s) => {
              const active = s.id === currentId
              return (
                <div
                  key={s.id}
                  onClick={() => onResume(s.id)}
                  title={s.title}
                  className={`group flex items-center gap-2.5 px-[11px] py-[9px] rounded-field cursor-pointer text-[14px] mb-0.5 ${
                    active ? 'text-ink bg-surface2' : 'text-muted hover:bg-surface2'
                  }`}
                >
                  <Icon name="file" size={15} />
                  <span className="flex-1 min-w-0 truncate">{s.title}</span>
                  <span className="text-faint text-[11px] flex-none group-hover:hidden">{s.when}</span>
                  {/* 当前会话不提供删除:后端会拒绝(删掉后下一个回合会把文件重建成
                      只含新内容的僵尸会话),这里直接不给入口,免得引到一条错误提示上。 */}
                  <button
                    type="button"
                    disabled={active}
                    title={active ? '当前会话不可删除；请先新建或切换到其它会话' : '删除此会话（不可恢复）'}
                    onClick={(e) => {
                      e.stopPropagation()
                      setConfirmDel(s)
                    }}
                    className="hidden group-hover:inline-flex flex-none text-faint hover:text-red leading-none px-0.5 disabled:opacity-30 disabled:hover:text-faint disabled:cursor-default"
                  >
                    ✕
                  </button>
                </div>
              )
            })
          )}
        </div>
      )}
      <div className="relative mt-3 flex-none">
        <button
          onClick={() => { if (collapsed) { onSwitchWorkspace() } else { setWsQuery(''); setWsOpen((o) => !o) } }}
          title={`当前工作区:${cwd || '—'}\n点击在其它工作区开一条会话（历史工作区可搜索）；已开着的会话不受影响`}
          className={`w-full flex items-center py-2.5 rounded-btn border border-line2 bg-surface hover:border-primary hover:bg-surface2 text-muted hover:text-ink transition ${
            collapsed ? 'justify-center' : 'gap-2 px-[11px]'
          }`}
        >
          <Icon name="folder" size={16} />
          {!collapsed && (
            <>
              <span className="flex-1 min-w-0 truncate text-left font-mono text-[13px]">{wsName}</span>
              <span className="text-faint text-[11px] flex-none">新开</span>
            </>
          )}
        </button>
        <Popover open={wsOpen && !collapsed} onClose={() => setWsOpen(false)} placement="up-full" variant="panel" className="max-h-[340px]">
          <div className="p-2.5 border-b border-line">
            <input
              autoFocus
              value={wsQuery}
              onChange={(e) => setWsQuery(e.target.value)}
              placeholder="搜索历史工作区…"
              className="w-full font-sans text-[13px] bg-surface2 text-ink border border-line2 rounded-field px-3 py-2 outline-none focus:border-primary"
            />
          </div>
          <div className="overflow-y-auto py-1">
            {wsMatches.map((w) => (
              <button
                key={w}
                type="button"
                title={w}
                onClick={() => { setWsOpen(false); onPickWorkspace(w) }}
                className="w-full text-left px-3.5 py-2 flex items-center gap-2 hover:bg-surface2 text-ink"
              >
                <Icon name="folder" size={14} />
                <span className="font-mono text-[13px] truncate flex-1">{w}</span>
              </button>
            ))}
            {wsMatches.length === 0 && (
              <div className="px-3.5 py-4 text-center text-[12px] text-muted">{recentWorkspaces.length === 0 ? '暂无历史工作区' : '没有匹配的工作区'}</div>
            )}
            <button
              type="button"
              onClick={() => { setWsOpen(false); onSwitchWorkspace() }}
              className="w-full text-left px-3.5 py-2 flex items-center gap-2 hover:bg-surface2 text-primary border-t border-line mt-1"
            >
              <Icon name="folder" size={14} />
              <span className="text-[13px]">浏览其它目录…</span>
            </button>
          </div>
        </Popover>
      </div>
      {/* 用户区:工作区切换器下方——记忆/权限/设置,以及退出登录。
          **未登录也渲染**:免登录进来的本地会话没有通行证用户,但设置、权限、记忆
          照样要够得着。这块曾经整个挂在 userName 上,那时它只装着一个退出登录,
          没有用户自然就没有内容;现在装了三个与登录无关的入口,再挂着就等于免登录
          用户彻底进不去设置页。只有「退出登录」那一条跟着登录态走。 */}
      <div className="relative mt-2 flex-none">
        <button
          type="button"
          onClick={() => setProfileOpen((o) => !o)}
          title={collapsed ? (userName || '设置') : undefined}
          className={`w-full flex items-center py-2 rounded-btn transition ${
            collapsed ? 'justify-center' : 'gap-2.5 px-[9px]'
          } ${profileActive ? 'bg-primarysoft text-primaryink' : 'text-muted hover:bg-surface2 hover:text-ink'}`}
        >
          <span className="relative flex-none">
            {userName
              ? <Avatar name={userName} avatar={avatar} />
              // 未登录时没有头像可画,用齿轮——这一栏此刻的全部内容就是设置类入口。
              : <span className="w-7 h-7 flex-none rounded-full bg-surface2 border border-line2 flex items-center justify-center"><Icon name="settings" size={15} /></span>}
            {/* 红点挂在头像上而不是那一行的末尾:侧栏折叠之后整行只剩这个头像,
                挂在别处就跟着一起没了——而折叠状态恰恰是最需要它的时候。
                描边用侧栏自己的底色,叠在深色头像上也分得开。 */}
            {hasUpdate && (
              <span
                className="absolute -top-0.5 -right-0.5 w-2.5 h-2.5 rounded-full bg-red border-2 border-surface"
                aria-hidden
              />
            )}
          </span>
          {!collapsed && (
            <>
              <span className={`flex-1 min-w-0 truncate text-left text-[13px] ${profileActive ? 'text-primaryink font-medium' : 'text-ink'}`}>
                {userName || '设置'}
              </span>
              <Icon name="more" size={16} className="text-faint" />
            </>
          )}
        </button>
        <Popover
          open={profileOpen}
          onClose={() => setProfileOpen(false)}
          placement={collapsed ? 'up-left' : 'up-full'}
          variant="menu"
          className={collapsed ? 'w-[152px]' : undefined}
        >
          {PROFILE_NAV.map((n) => (
            <button
              key={n.view}
              type="button"
              onClick={() => { setProfileOpen(false); onNav(n.view) }}
              className={`w-full text-left px-3.5 py-2 flex items-center gap-2.5 text-[13px] hover:bg-surface2 ${
                view === n.view ? 'text-primaryink font-medium bg-primarysoft' : 'text-ink'
              }`}
            >
              <Icon name={n.name} size={15} />
              {n.label}
              {n.view === 'settings' && hasUpdate && (
                <span className="ml-auto inline-flex items-center gap-1.5 text-[12px] text-red">
                  <span className="w-1.5 h-1.5 rounded-full bg-red" />
                  有新版本
                </span>
              )}
            </button>
          ))}
          {userName && (
            <>
              {/* 分隔线:退出登录是**离开**,和上面三个"去某一页"不是一回事,
                  挨着排会被顺手点到。 */}
              <div className="h-px bg-line2 my-1" />
              <button
                type="button"
                onClick={() => { setProfileOpen(false); onLogout() }}
                className="w-full text-left px-3.5 py-2 flex items-center gap-2.5 text-[13px] text-ink hover:bg-surface2 hover:text-red"
              >
                <Icon name="logout" size={15} />
                退出登录
              </button>
            </>
          )}
        </Popover>
      </div>
      {confirmDel && (
        <ConfirmDialog
          title="删除会话"
          message={<>确定删除会话「<b className="text-ink font-semibold">{confirmDel.title}</b>」？此操作不可撤销。</>}
          confirmLabel="删除"
          onConfirm={() => { onDelete(confirmDel.id); setConfirmDel(null) }}
          onCancel={() => setConfirmDel(null)}
        />
      )}
      {confirmClose && (
        <ConfirmDialog
          title="关闭正在运行的会话"
          message={<>「<b className="text-ink font-semibold">{confirmClose.title}</b>」还有任务在跑，关闭会当场中断它。要关闭吗？</>}
          confirmLabel="关闭"
          onConfirm={() => { onCloseSession(confirmClose.id); setConfirmClose(null) }}
          onCancel={() => setConfirmClose(null)}
        />
      )}
    </aside>
  )
}

// Avatar 优先渲染通行证头像;缺失或加载失败(外链取不到)回落到用户名首字的色块,
// 保证离线/无头像时也有稳定的占位,不会出现裂图。
function Avatar({ name, avatar }: { name: string; avatar?: string }) {
  const [broken, setBroken] = useState(false)
  const initial = name.trim().slice(0, 1).toUpperCase() || '?'
  if (avatar && !broken) {
    return (
      <img
        src={avatar}
        alt=""
        draggable={false}
        onError={() => setBroken(true)}
        className="w-7 h-7 rounded-full object-cover flex-none select-none bg-surface2"
      />
    )
  }
  return (
    <span className="w-7 h-7 rounded-full bg-primary text-white text-[13px] font-semibold inline-flex items-center justify-center flex-none select-none">
      {initial}
    </span>
  )
}
