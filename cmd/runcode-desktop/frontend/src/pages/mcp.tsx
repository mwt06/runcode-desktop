// MCPPage manages the Model Context Protocol servers stored in the shared
// config.toml (the same file the CLI reads), and shows each server's live
// connection state from the running session.
import { useEffect, useState } from 'react'
import { Icon } from '@/ui/icons'
import { BTN, BTN_PRIMARY, BTN_DANGER } from '@/ui/tokens'
import { FIELD_CLS, LABEL_CLS } from '@/ui/fields'
import {
  deleteMCPServer, errText, listMCPServers, mcpMarket, reloadMCPServers, saveMCPServer,
  setMCPServerEnabled, type McpMarketEntry, type MCPServerInfo,
} from '@/core/bridge'
import { draftFrom, toServerInput, type MCPDraft } from './mcp-draft'
import { installState, marketEntryToInput } from './mcp-market'
import { InlineError } from '@/ui/feedback'
import { PageShell, Placeholder } from '@/ui/layout'

export function MCPPage() {
  const [servers, setServers] = useState<MCPServerInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [draft, setDraft] = useState<MCPDraft | null>(null)
  const [saving, setSaving] = useState(false)
  // Market ("市场") tab: the platform-served installable list. Separate loading/
  // error state from the installed list so one tab's failure never blanks the other.
  const [mode, setMode] = useState<'installed' | 'market'>('installed')
  const [market, setMarket] = useState<McpMarketEntry[]>([])
  const [marketLoading, setMarketLoading] = useState(false)
  const [marketError, setMarketError] = useState('')
  const [installing, setInstalling] = useState<string | null>(null)
  // Which connected servers have their tool list expanded on the page.
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const mono = FIELD_CLS + ' font-mono text-[13px] leading-relaxed'

  async function refresh() {
    setLoading(true)
    setError('')
    try {
      setServers((await listMCPServers()) ?? [])
    } catch (e) {
      setError(errText(e))
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void refresh()
    // 拉一次市场清单:后端借此把基座自建服务的通行证标志同步到本地配置(用户不必
    // 做任何勾选,旧版本装的条目也会被补上),同步后再刷新列表让徽标即时出现。
    // 未登录/离线时静默失败——已安装列表照常显示。
    mcpMarket().then(() => refresh()).catch(() => {})
  }, [])
  // Load the platform market when its tab opens; re-fetch on each open so a bridge
  // config change shows up without restarting the app.
  useEffect(() => {
    if (mode === 'market') void loadMarket()
  }, [mode])

  // applied 在配置改动后让它当场生效:重建正在跑的会话(按原会话 id 恢复,对话不丢)
  // 并重连所有服务器,然后刷新列表——安装完就能看到「已连接 · N 个工具」,不用手动
  // 新建会话。没有活动会话时它什么都不做(下次会话自然读到新配置);回合进行中会被
  // 拒绝,把原因显示出来而不是假装已生效。
  async function applied(mutate: () => Promise<unknown>, setBusy?: (v: boolean) => void) {
    setBusy?.(true)
    setError('')
    try {
      await mutate()
      try {
        await reloadMCPServers()
      } catch (e) {
        setError(errText(e)) // 改动已存盘,只是没能立刻生效
      }
      await refresh()
    } catch (e) {
      setError(errText(e))
    } finally {
      setBusy?.(false)
    }
  }

  const save = () =>
    draft &&
    applied(async () => {
      await saveMCPServer(toServerInput(draft))
      setDraft(null)
    }, setSaving)
  const remove = (name: string) => applied(() => deleteMCPServer(name))
  const toggle = (s: MCPServerInfo) => applied(() => setMCPServerEnabled(s.name, !s.enabled))

  async function loadMarket() {
    setMarketLoading(true)
    setMarketError('')
    try {
      setMarket((await mcpMarket()) ?? [])
    } catch (e) {
      setMarketError(errText(e))
    } finally {
      setMarketLoading(false)
    }
  }
  // Install = write the entry into config.toml via the same SaveMCPServer path a
  // manual add uses, then refresh the installed list so the button flips to 已安装.
  async function install(e: McpMarketEntry) {
    setInstalling(e.id)
    setMarketError('')
    try {
      await saveMCPServer(marketEntryToInput(e))
      // 装完立刻连上(重建会话、保留对话),用户切回「已安装」就能看到工具数。
      await reloadMCPServers().catch((err) => setMarketError(errText(err)))
      await refresh()
    } catch (err) {
      setMarketError(errText(err))
    } finally {
      setInstalling(null)
    }
  }

  const set = (patch: Partial<MCPDraft>) => setDraft((d) => (d ? { ...d, ...patch } : d))

  return (
    <PageShell
      title="MCP 服务器"
      hint={<>连接外部工具服务器(Model Context Protocol)。与命令行共用同一份 <code className="font-mono text-[12px] bg-surface2 px-1 py-0.5 rounded">config.toml</code>,更改<b className="text-ink font-semibold">立即生效</b>(会重连当前会话,对话不受影响;回合进行中则等本轮结束)。</>}
      action={mode === 'installed' && !draft && (
        <button className={`${BTN} ${BTN_PRIMARY} flex-none`} onClick={() => setDraft(draftFrom())}>
          <Icon name="plus" size={15} /> 新建
        </button>
      )}
    >

        <div className="inline-flex self-start rounded-btn border border-line2 bg-surface2 p-0.5 text-[13px]">
          <button
            className={`px-3.5 py-1.5 rounded-lg transition-colors ${mode === 'installed' ? 'bg-surface text-ink shadow-xs font-medium' : 'text-muted hover:text-ink'}`}
            onClick={() => setMode('installed')}
          >已安装{servers.length ? ` · ${servers.length}` : ''}</button>
          <button
            className={`px-3.5 py-1.5 rounded-lg transition-colors ${mode === 'market' ? 'bg-surface text-ink shadow-xs font-medium' : 'text-muted hover:text-ink'}`}
            onClick={() => setMode('market')}
          >市场</button>
        </div>

        {mode === 'installed' ? (
        <>
        {error && <InlineError>{error}</InlineError>}

        {draft ? (
          <section className="bg-surface border border-line2 rounded-card p-5 flex flex-col gap-3.5 shadow-xs">
            <div className="text-[14px] font-semibold">{draft.originalName ? `编辑 ${draft.originalName}` : '新建 MCP 服务器'}</div>
            <div className="grid grid-cols-2 gap-3">
              <label className={LABEL_CLS}>名称<input className={FIELD_CLS} value={draft.name} onChange={(e) => set({ name: e.target.value })} placeholder="例如 filesystem" /></label>
              <label className={LABEL_CLS}>传输方式
                <select className={FIELD_CLS} value={draft.transport} onChange={(e) => set({ transport: e.target.value })}>
                  <option value="stdio">stdio(本地进程)</option>
                  <option value="http">http(远程端点)</option>
                </select>
              </label>
            </div>
            {draft.transport === 'http' ? (
              <>
                <label className={LABEL_CLS}>URL<input className={FIELD_CLS} value={draft.url} onChange={(e) => set({ url: e.target.value })} placeholder="https://example.com/mcp" /></label>
                <label className={LABEL_CLS}>请求头(每行 KEY=VALUE,值可用 ${'{ENV_VAR}'})<textarea className={mono} rows={2} value={draft.headersText} onChange={(e) => set({ headersText: e.target.value })} placeholder="Authorization=Bearer ${TOKEN}" /></label>
                {/* 通行证注入不设手动开关：由基座下发的市场清单决定（见 pages/mcp-market）。
                    这里只在已开启时告知,编辑保存不会丢掉这个标志。 */}
                {draft.passport && (
                  <div className="text-[12px] text-primaryink bg-primarysoft border border-primary/30 rounded-lg px-3 py-2">
                    基座自建服务:每次请求自动带上你的登录通行证与所选租户,仅返回你本人的数据。
                  </div>
                )}
              </>
            ) : (
              <>
                <label className={LABEL_CLS}>命令<input className={FIELD_CLS} value={draft.command} onChange={(e) => set({ command: e.target.value })} placeholder="npx" /></label>
                <label className={LABEL_CLS}>参数(每行一个)<textarea className={mono} rows={3} value={draft.argsText} onChange={(e) => set({ argsText: e.target.value })} placeholder={"-y\n@modelcontextprotocol/server-filesystem"} /></label>
                <div className="grid grid-cols-2 gap-3">
                  <label className={LABEL_CLS}>环境变量(每行 KEY=VALUE)<textarea className={mono} rows={2} value={draft.envText} onChange={(e) => set({ envText: e.target.value })} placeholder="TOKEN=${MY_TOKEN}" /></label>
                  <label className={LABEL_CLS}>工作目录(可选)<input className={FIELD_CLS} value={draft.dir} onChange={(e) => set({ dir: e.target.value })} placeholder="留空则用工作区" /></label>
                </div>
              </>
            )}
            <label className="flex items-center gap-2.5 text-[13px] text-ink cursor-pointer select-none">
              <input type="checkbox" checked={draft.enabled} onChange={(e) => set({ enabled: e.target.checked })} className="w-4 h-4 accent-[var(--color-primary)]" />
              启用(会话启动时连接此服务器)
            </label>
            <div className="text-[12px] text-faint -mt-1">密钥请用 <code className="font-mono bg-surface2 px-1 py-0.5 rounded">${'{ENV_VAR}'}</code> 引用,只把变量名写进配置文件,明文密钥留在环境变量里。</div>
            <div className="flex gap-2.5 mt-1">
              <button className={`${BTN} ${BTN_PRIMARY}`} disabled={!draft.name.trim() || saving} onClick={save}>{saving ? '保存中…' : '保存'}</button>
              <button className={BTN} onClick={() => { setDraft(null); setError('') }}>取消</button>
            </div>
          </section>
        ) : loading ? (
          <Placeholder>加载中…</Placeholder>
        ) : servers.length === 0 ? (
          <div className="bg-surface border border-line2 border-dashed rounded-card px-5 py-10 text-center">
            <div className="text-muted text-[14px]">还没有配置 MCP 服务器</div>
            <div className="text-faint text-[13px] mt-1">点右上角「新建」接入一个外部工具服务器(如文件系统、浏览器、数据库)。</div>
          </div>
        ) : (
          <div className="flex flex-col gap-2.5">
            {servers.map((s) => (
              <div key={s.name} className="bg-surface border border-line2 rounded-card p-4 shadow-xs flex flex-col gap-2">
                <div className="flex items-center gap-2.5">
                  <span className={`w-2 h-2 rounded-full flex-none ${!s.enabled ? 'bg-faint' : s.connected ? 'bg-green' : 'bg-amber'}`} />
                  <span className="font-semibold text-[15px]">{s.name}</span>
                  <span className="font-mono text-[11px] uppercase tracking-wide text-muted bg-surface2 border border-line2 rounded px-1.5 py-0.5">{s.transport}</span>
                  {s.passport && <span className="text-[11px] px-1.5 py-0.5 rounded bg-primarysoft text-primaryink border border-primary/40">通行证</span>}
                  <span className="text-[12px] text-muted ml-1">
                    {!s.enabled ? '已停用' : s.connected ? `已连接 · ${s.toolCount} 个工具` : '未连接'}
                  </span>
                  <div className="ml-auto flex items-center gap-1.5">
                    <button className={`${BTN} px-2.5 py-1 text-[13px]`} onClick={() => toggle(s)}>{s.enabled ? '停用' : '启用'}</button>
                    <button className={`${BTN} px-2.5 py-1 text-[13px]`} onClick={() => setDraft(draftFrom(s))}>编辑</button>
                    <button className={`${BTN} ${BTN_DANGER} px-2.5 py-1 text-[13px]`} onClick={() => remove(s.name)}>删除</button>
                  </div>
                </div>
                <div className="font-mono text-[12px] text-muted break-all pl-[18px]">
                  {s.transport === 'http' ? s.url : [s.command, ...(s.args ?? [])].filter(Boolean).join(' ')}
                </div>
                {s.tools && s.tools.length > 0 && (
                  <div className="pl-[18px]">
                    <button
                      className="text-[12px] text-primaryink hover:underline"
                      onClick={() => setExpanded((m) => ({ ...m, [s.name]: !m[s.name] }))}
                    >
                      {expanded[s.name] ? '收起工具' : `查看 ${s.tools.length} 个工具`}
                    </button>
                    {expanded[s.name] && (
                      <div className="mt-2 flex flex-col gap-1.5 border-l-2 border-line2 pl-3">
                        {s.tools.map((t) => (
                          <div key={t.name} className="text-[12px] leading-snug">
                            <span className="font-mono text-ink font-medium">{t.name}</span>
                            {t.description && <span className="text-faint"> — {t.description}</span>}
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
        </>
        ) : marketError ? (
          <div className="flex flex-col gap-3">
            <InlineError>{marketError}</InlineError>
            <button className={`${BTN} self-start`} onClick={() => void loadMarket()}>重试</button>
          </div>
        ) : marketLoading ? (
          <Placeholder>加载中…</Placeholder>
        ) : market.length === 0 ? (
          <div className="bg-surface border border-line2 border-dashed rounded-card px-5 py-10 text-center">
            <div className="text-muted text-[14px]">市场暂无可安装的 MCP</div>
            <div className="text-faint text-[13px] mt-1">基座下发的清单为空,或当前未登录通行证。</div>
          </div>
        ) : (
          <div className="flex flex-col gap-2.5">
            <p className="text-faint text-[13px]">由基座下发的可安装服务器。安装即写入 <code className="font-mono text-[12px] bg-surface2 px-1 py-0.5 rounded">config.toml</code> 并<b className="text-ink font-semibold">立即连接</b>,随后在「已安装」里就能看到它的工具。</p>
            {market.map((e) => {
              const state = installState(e, servers)
              return (
                <div key={e.id} className="bg-surface border border-line2 rounded-card p-4 shadow-xs flex flex-col gap-2">
                  <div className="flex items-center gap-2.5 flex-wrap">
                    <span className="font-semibold text-[15px]">{e.name}</span>
                    {e.official && (
                      <span className="text-[11px] px-1.5 py-0.5 rounded bg-primarysoft text-primaryink border border-primary/40">基座自建</span>
                    )}
                    <span className="font-mono text-[11px] uppercase tracking-wide text-muted bg-surface2 border border-line2 rounded px-1.5 py-0.5">{e.transport}</span>
                    <button
                      className={`${BTN} px-3 py-1 text-[13px] ml-auto ${state === 'installed' ? '' : BTN_PRIMARY}`}
                      disabled={state === 'installed' || installing === e.id}
                      onClick={() => void install(e)}
                    >
                      {installing === e.id ? '安装中…' : state === 'installed' ? '已安装' : state === 'outdated' ? '更新' : '安装'}
                    </button>
                  </div>
                  {e.description && <div className="text-[13px] text-muted leading-relaxed">{e.description}</div>}
                  <div className="font-mono text-[12px] text-muted break-all">{e.url}</div>
                  {e.passport && (
                    <div className="text-[12px] text-muted">已随基座自动注入登录通行证,仅返回你本人在该系统里的数据——无需手动配置。</div>
                  )}
                  {state === 'outdated' && (
                    <div className="text-[12px] text-amber">已安装的配置与基座下发的不一致(如地址已变更),点「更新」同步。</div>
                  )}
                </div>
              )
            })}
          </div>
        )}
    </PageShell>
  )
}
