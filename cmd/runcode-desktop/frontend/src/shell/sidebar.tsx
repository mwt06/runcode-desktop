import { useState } from 'react'
import { Icon } from '@/ui/icons'
import { ConfirmDialog } from '@/ui/confirm-dialog'
import { Popover } from '@/ui/popover'
import { type SessionSummary } from '@/core/bridge'

// Sidebar 是主界面左栏：新建对话、页面导航、最近对话列表与工作区切换器。
// 全部数据与动作经 props 注入（自身只持有弹层/确认框的局部状态），与 App 的
// 会话状态解耦——从 App.tsx 原样搬出，行为不变。
export function Sidebar({
  collapsed,
  recents,
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
}: {
  collapsed: boolean
  recents: SessionSummary[]
  currentId?: string
  cwd?: string
  recentWorkspaces: string[]
  onPickWorkspace: (path: string) => void
  onSwitchWorkspace: () => void
  onDelete: (id: string) => void
  view: 'chat' | 'settings' | 'plugins' | 'permissions' | 'memory'
  onNav: (v: 'chat' | 'settings' | 'plugins' | 'permissions' | 'memory') => void
  onNew: () => void
  onResume: (id: string) => void
}) {
  // Workspace switcher popover: search + recent-workspace list + browse.
  const [wsOpen, setWsOpen] = useState(false)
  const [wsQuery, setWsQuery] = useState('')
  // 待确认删除的会话（打开自定义确认弹窗，替代原生 window.confirm）。
  const [confirmDel, setConfirmDel] = useState<SessionSummary | null>(null)
  const wsq = wsQuery.trim().toLowerCase()
  const wsMatches = recentWorkspaces.filter((w) => w && w !== cwd && (!wsq || w.toLowerCase().includes(wsq)))
  // 折叠成图标栏（而非整块隐藏）：导航/新建/工作区仍可点，只收起文字与最近对话。
  // collapsed 由父组件（App）提供——折叠开关已移到主栏顶部状态条（「空闲」前）。
  const wsName = cwd ? cwd.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || cwd : '—'
  const nav = [
    { label: '对话', name: 'chat', view: 'chat' as const },
    { label: '插件', name: 'grid', view: 'plugins' as const },
    { label: '记忆', name: 'file', view: 'memory' as const },
    { label: '权限', name: 'shield', view: 'permissions' as const },
    { label: '设置', name: 'settings', view: 'settings' as const },
  ]
  return (
    <aside
      className={`${collapsed ? 'w-[64px] px-2.5' : 'w-[268px] px-4'} py-4 flex-none bg-surface border-r border-line2 flex flex-col transition-[width] duration-200 ease-out`}
    >
      <button
        className={`w-full border-none bg-primary text-white font-semibold text-sm rounded-[11px] cursor-pointer inline-flex items-center justify-center gap-2 shadow-[0_5px_14px_rgba(91,108,240,0.3)] hover:brightness-105 transition ${collapsed ? 'h-10' : 'py-3'}`}
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
            className={`flex items-center py-[9px] rounded-[9px] text-sm cursor-pointer select-none ${
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
          <div className="text-[11.5px] text-faint px-[11px] pb-2 tracking-wide">最近对话</div>
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
                  className={`group flex items-center gap-2.5 px-[11px] py-[9px] rounded-[9px] cursor-pointer text-[13.5px] mb-0.5 ${
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
          title={`当前工作区:${cwd || '—'}\n点击切换（历史工作区可搜索）`}
          className={`w-full flex items-center py-2.5 rounded-[10px] border border-line2 bg-surface hover:border-primary hover:bg-surface2 text-muted hover:text-ink transition ${
            collapsed ? 'justify-center' : 'gap-2 px-[11px]'
          }`}
        >
          <Icon name="folder" size={16} />
          {!collapsed && (
            <>
              <span className="flex-1 min-w-0 truncate text-left font-mono text-[12.5px]">{wsName}</span>
              <span className="text-faint text-[11px] flex-none">切换</span>
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
              className="w-full font-sans text-[13px] bg-surface2 text-ink border border-line2 rounded-[9px] px-3 py-2 outline-none focus:border-primary"
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
                <span className="font-mono text-[12.5px] truncate flex-1">{w}</span>
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
              <span className="text-[12.5px]">浏览其它目录…</span>
            </button>
          </div>
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
    </aside>
  )
}
