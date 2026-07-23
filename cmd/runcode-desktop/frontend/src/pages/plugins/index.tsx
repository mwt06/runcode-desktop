// PluginsPage 是统一的能力管理页：标签页切换 工具/子代理/技能/MCP，顶部选停用
// 范围 + 搜索，每行图标+名称+描述+iOS 开关。详情为全屏单页(AgentDetail /
// SkillDetail)，MCP 直接内嵌 MCPPage。
import { useEffect, useState } from 'react'
import { Icon, TOOL_ICON } from '@/ui/icons'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { Popover } from '@/ui/popover'
import { Toggle } from '@/ui/toggle'
import { sourceLabel } from '@/ui/badges'
import { BUILTIN_TOOLS } from '@/core/tool-catalog'
import {
  errText,
  importAgent, importSkill,
  listAgents, listSkills, listTools,
  setAgentEnabled, setSkillEnabled, setToolEnabled,
  type AgentInfo, type SkillInfo, type ToolInfo,
} from '@/core/bridge'
import { MCPPage } from '../mcp'
import { BUILTIN_AGENTS } from './builtin-agents'
import { AgentDetail } from './agent-detail'
import { SkillDetail } from './skill-detail'

export function PluginsPage({ onUseSkill, onUseAgent }: { onUseSkill: (name: string) => void; onUseAgent: (name: string) => void }) {
  const [tab, setTab] = useState<'tools' | 'agents' | 'skills' | 'mcp'>('tools')
  const [scope, setScope] = useState<'project' | 'user'>('project')
  const [query, setQuery] = useState('')
  const [toolList, setToolList] = useState<ToolInfo[]>([])
  const [agentList, setAgentList] = useState<AgentInfo[]>([])
  const [skillList, setSkillList] = useState<SkillInfo[]>([])
  const [err, setErr] = useState('')
  const [detail, setDetail] = useState<{ k: 'agent'; item: AgentInfo | 'new' } | { k: 'skill'; item: SkillInfo | 'new' } | null>(null)
  // 导入范围显式选择(不复用上面的「停用范围」——那个只管开关的作用域)，
  // 与「新建」在详情页里选范围的做法保持一致。
  const [importMenu, setImportMenu] = useState(false)

  const reloadTools = async () => { try { setToolList((await listTools()) ?? []) } catch { /* ignore */ } }
  const reloadAgents = async () => { try { const l = await listAgents(); setAgentList(l?.agents ?? []) } catch { /* ignore */ } }
  const reloadSkills = async () => { try { const l = await listSkills(); setSkillList(l?.skills ?? []) } catch { /* ignore */ } }
  useEffect(() => { void reloadTools(); void reloadAgents(); void reloadSkills() }, [])

  // 导入当前标签页对应的能力：弹原生选择器(技能选整个文件夹连同相关文件，子代理选 .md)，
  // 校验后写入所选范围。取消选择不报错(后端原样返回当前列表)。
  const doImport = async (s: 'project' | 'user') => {
    setImportMenu(false)
    setErr('')
    try {
      if (tab === 'skills') setSkillList((await importSkill(s))?.skills ?? [])
      else setAgentList((await importAgent(s))?.agents ?? [])
    } catch (e) {
      setErr(errText(e))
    }
  }

  // 详情为全屏单页(替代左右分栏)。
  if (detail?.k === 'agent') return <AgentDetail agent={detail.item} onBack={() => setDetail(null)} onChanged={reloadAgents} onUse={onUseAgent} />
  if (detail?.k === 'skill') return <SkillDetail skill={detail.item} onBack={() => setDetail(null)} onChanged={reloadSkills} onUse={onUseSkill} />

  const scopeOn = (du: boolean, dp: boolean) => (scope === 'project' ? !dp : !du)
  const otherOffLabel = (du: boolean, dp: boolean) => (scope === 'project' ? (du ? '全局已停用' : '') : dp ? '本项目已停用' : '')
  const toggle = (setFn: (n: string, s: string, e: boolean) => Promise<void>, reload: () => Promise<void>) => async (name: string, next: boolean) => {
    setErr('')
    try { await setFn(name, scope, next); await reload() } catch (e) { setErr(errText(e)) }
  }
  const toggleTool = toggle(setToolEnabled, reloadTools)
  const toggleAgent = toggle(setAgentEnabled, reloadAgents)
  const toggleSkill = toggle(setSkillEnabled, reloadSkills)

  const q = query.trim().toLowerCase()
  const hit = (a: string, b: string, name: string) => !q || name.toLowerCase().includes(q) || a.toLowerCase().includes(q) || b.toLowerCase().includes(q)
  // This page manages only user/project-authored capabilities; built-ins are the
  // engine's fixed, read-only set, so they are hidden here (the model still uses
  // them, and the composer's / picker still lists built-in agents to delegate).
  // 全局(用户级)范围下再隐藏项目级条目:用户级停用是按名字记进全局 disabled.json
  // 的,对只存在于本工作区的项目级子代理/技能做「全局停用」既无意义,还会误伤
  // 其它项目的同名条目;切到「本项目」范围即可管理它们。
  const inScope = (source: string) => scope === 'project' || source !== 'project'
  const scopedAgents = agentList.filter((a) => a.source !== 'builtin' && inScope(a.source))
  const scopedSkills = skillList.filter((s) => inScope(s.source))
  const shownTools = toolList.filter((t) => t.toggleable && t.source !== 'builtin').filter((t) => { const z = BUILTIN_TOOLS[t.name]; return hit(z?.label ?? '', z?.desc ?? t.description, t.name) })
  const shownAgents = scopedAgents.filter((a) => hit('', a.description, a.name))
  const shownSkills = scopedSkills.filter((s) => hit(s.name, s.description, s.name))

  const tabs: { k: typeof tab; label: string; n?: number }[] = [
    { k: 'tools', label: '工具', n: toolList.filter((t) => t.toggleable && t.source !== 'builtin').length },
    { k: 'agents', label: '子代理', n: scopedAgents.length },
    { k: 'skills', label: '技能', n: scopedSkills.length },
    { k: 'mcp', label: 'MCP' },
  ]
  const showControls = tab !== 'mcp'
  const scopeBtn = (s: 'project' | 'user', text: string) => (
    <button type="button" onClick={() => setScope(s)} className={`px-3.5 py-1 text-[12.5px] transition ${scope === s ? 'bg-primary text-white' : 'text-muted hover:text-ink'}`}>{text}</button>
  )

  // 一张能力卡片：图标 + 名称(+原名/徽章) + 描述 + [使用] + iOS 开关。可点击项进入详情。
  const card = (key: string, icon: string, title: string, raw: string, tag: string, desc: string, on: boolean, otherOff: string, onToggle: (n: boolean) => void, onClick?: () => void, onUse?: () => void) => (
    <div
      key={key}
      onClick={onClick}
      className={`bg-surface border border-line2 rounded-[14px] px-5 py-4 flex items-center gap-4 transition ${onClick ? 'cursor-pointer hover:border-primary hover:shadow-xs' : ''} ${on ? '' : 'opacity-60'}`}
    >
      <span className="w-10 h-10 rounded-[11px] bg-surface2 border border-line2 flex items-center justify-center flex-none text-muted"><Icon name={icon} size={19} /></span>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="font-semibold text-[14.5px] text-ink truncate">{title}</span>
          {raw && <span className="font-mono text-[11.5px] text-faint flex-none">{raw}</span>}
          {tag && <span className="text-[10.5px] text-faint border border-line2 rounded px-1.5 py-px flex-none">{tag}</span>}
        </div>
        <div className="text-[12.5px] text-muted mt-1 line-clamp-2 leading-relaxed">{desc}</div>
      </div>
      {otherOff && <span className="text-[11px] text-red/70 flex-none">{otherOff}</span>}
      {onUse && <button type="button" className="text-[12px] text-muted hover:text-primary flex-none" onClick={(e) => { e.stopPropagation(); onUse() }}>使用</button>}
      <Toggle on={on} onChange={onToggle} />
    </div>
  )

  return (
    <div className="flex-1 flex flex-col min-h-0">
      <div className="flex-none px-10 pt-9 pb-5 border-b border-line">
        <div className="max-w-[1080px] mx-auto">
          <h2 className="m-0 text-[22px] font-bold tracking-tight">插件</h2>
          <p className="mt-1.5 text-muted text-[13px]">管理工具、子代理、技能与 MCP · 关闭的不传给模型</p>
          <div className="flex items-center gap-3 mt-5">
            <div className="flex items-center gap-1">
              {tabs.map((t) => (
                <button
                  key={t.k}
                  type="button"
                  onClick={() => setTab(t.k)}
                  className={`px-3.5 py-1.5 rounded-[9px] text-[13.5px] transition ${tab === t.k ? 'bg-surface2 text-ink font-medium' : 'text-muted hover:text-ink'}`}
                >
                  {t.label}{t.n !== undefined && <span className="ml-1.5 text-faint text-[12px]">{t.n}</span>}
                </button>
              ))}
            </div>
            {showControls && (
              <div className="ml-auto relative">
                <span className="absolute left-3 top-1/2 -translate-y-1/2 text-faint pointer-events-none"><Icon name="search" size={14} /></span>
                <input className="w-[240px] font-sans text-[13px] bg-surface2 text-ink border border-line2 rounded-[10px] pl-9 pr-3 py-2 outline-none focus:border-primary" value={query} onChange={(e) => setQuery(e.target.value)} placeholder="搜索" />
              </div>
            )}
            {tab === 'agents' && <button type="button" className={`${BTN} ${BTN_PRIMARY} px-4 py-2 text-[13px]`} onClick={() => setDetail({ k: 'agent', item: 'new' })}>+ 新建</button>}
            {tab === 'skills' && <button type="button" className={`${BTN} ${BTN_PRIMARY} px-4 py-2 text-[13px]`} onClick={() => setDetail({ k: 'skill', item: 'new' })}>+ 新建</button>}
            {(tab === 'skills' || tab === 'agents') && (
              <div className="relative flex-none">
                <button
                  type="button"
                  onClick={() => setImportMenu((v) => !v)}
                  title={tab === 'skills' ? '导入技能文件夹（含 SKILL.md 及相关文件）' : '从已有的 .md 导入子代理'}
                  className={`${BTN} px-4 py-2 text-[13px] inline-flex items-center gap-1.5 whitespace-nowrap`}
                >
                  <Icon name="book" size={15} /> 导入
                  <Icon name="chevron-down" size={12} />
                </button>
                <Popover open={importMenu} onClose={() => setImportMenu(false)} placement="down-right" className="w-[260px]">
                  <div onClick={() => void doImport('project')} className="px-3.5 py-2 cursor-pointer hover:bg-surface2">
                    <div className="text-[13px] text-ink">导入到本项目</div>
                    <div className="text-[11.5px] text-faint mt-0.5">仅当前工作区可用</div>
                  </div>
                  <div onClick={() => void doImport('user')} className="px-3.5 py-2 cursor-pointer hover:bg-surface2">
                    <div className="text-[13px] text-ink">导入到全局(用户级)</div>
                    <div className="text-[11.5px] text-faint mt-0.5">所有项目都可用</div>
                  </div>
                </Popover>
              </div>
            )}
          </div>
          {showControls && (
            <div className="flex items-center gap-3 mt-4 text-[12.5px]">
              <span className="text-muted">停用范围</span>
              <div className="inline-flex rounded-[9px] border border-line2 overflow-hidden">
                {scopeBtn('project', '本项目')}
                {scopeBtn('user', '全局(用户级)')}
              </div>
              <span className="text-faint">下次新建会话生效{scope === 'user' ? ' · 项目级条目请切到「本项目」范围管理' : ''}</span>
            </div>
          )}
          {err && <p className="mt-3 text-red text-[12.5px]">{err}</p>}
        </div>
      </div>

      {tab === 'mcp' ? (
        <MCPPage />
      ) : (
        <div className="flex-1 overflow-y-auto px-10 py-6">
          <div className="max-w-[1080px] mx-auto flex flex-col gap-3">
            {tab === 'tools' && shownTools.map((t) => {
              const z = BUILTIN_TOOLS[t.name]
              return card('t/' + t.name, TOOL_ICON[t.name] ?? 'grid', z?.label ?? t.name, z ? t.name : '', t.source === 'mcp' ? (t.server ?? 'MCP') : '', z?.desc ?? t.description, scopeOn(t.disabledUser, t.disabledProject), otherOffLabel(t.disabledUser, t.disabledProject), (n) => toggleTool(t.name, n))
            })}
            {tab === 'agents' && shownAgents.map((a) => {
              const z = a.source === 'builtin' ? BUILTIN_AGENTS[a.name] : undefined
              const badge = sourceLabel(a.source)
              // 内置子代理不可查看详情(只读)；用户/项目的点击进入编辑。所有子代理都可「使用」。
              const onClick = a.source === 'builtin' ? undefined : () => setDetail({ k: 'agent', item: a })
              return card('a/' + a.source + '/' + a.name, TOOL_ICON[a.name] ?? 'bot', z?.label ?? a.name, z ? a.name : '', badge, z?.desc ?? a.description, scopeOn(a.disabledUser, a.disabledProject), otherOffLabel(a.disabledUser, a.disabledProject), (n) => toggleAgent(a.name, n), onClick, () => onUseAgent(a.name))
            })}
            {tab === 'skills' && shownSkills.map((s) => {
              const badge = sourceLabel(s.source)
              return card('s/' + s.source + '/' + s.name, 'book', s.name, '', badge, s.description, scopeOn(s.disabledUser, s.disabledProject), otherOffLabel(s.disabledUser, s.disabledProject), (n) => toggleSkill(s.name, n), () => setDetail({ k: 'skill', item: s }))
            })}
            {tab === 'tools' && shownTools.length === 0 && <div className="text-center text-muted text-[13px] py-16">{q ? '没有匹配的工具' : '还没有自定义工具（内置工具已隐藏，模型仍可使用；连接 MCP 服务器可在此管理其工具）'}</div>}
            {tab === 'agents' && shownAgents.length === 0 && <div className="text-center text-muted text-[13px] py-16">{q ? '没有匹配的子代理' : '还没有自定义子代理，点右上「新建」创建（内置子代理已隐藏，仍可在对话中委派）'}</div>}
            {tab === 'skills' && shownSkills.length === 0 && <div className="text-center text-muted text-[13px] py-16">还没有技能，点右上「新建」创建，或「导入」一个已有的技能文件夹（含 SKILL.md 及相关文件）</div>}
          </div>
        </div>
      )}
    </div>
  )
}
