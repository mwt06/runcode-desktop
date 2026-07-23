// MCPPage manages the Model Context Protocol servers stored in the shared
// config.toml (the same file the CLI reads), and shows each server's live
// connection state from the running session.
import { useEffect, useState } from 'react'
import { Icon } from '@/ui/icons'
import { BTN, BTN_PRIMARY, BTN_DANGER } from '@/ui/tokens'
import { FIELD_CLS, LABEL_CLS } from '@/ui/fields'
import {
  deleteMCPServer, errText, listMCPServers, saveMCPServer, setMCPServerEnabled,
  type MCPServerInfo,
} from '@/core/bridge'
import { draftFrom, toServerInput, type MCPDraft } from './mcp-draft'

export function MCPPage() {
  const [servers, setServers] = useState<MCPServerInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [draft, setDraft] = useState<MCPDraft | null>(null)
  const [saving, setSaving] = useState(false)
  const mono = FIELD_CLS + ' font-mono text-[12.5px] leading-relaxed'

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
  }, [])

  async function save() {
    if (!draft) return
    setSaving(true)
    setError('')
    try {
      await saveMCPServer(toServerInput(draft))
      setDraft(null)
      await refresh()
    } catch (e) {
      setError(errText(e))
    } finally {
      setSaving(false)
    }
  }
  async function remove(name: string) {
    setError('')
    try {
      await deleteMCPServer(name)
      await refresh()
    } catch (e) {
      setError(errText(e))
    }
  }
  async function toggle(s: MCPServerInfo) {
    setError('')
    try {
      await setMCPServerEnabled(s.name, !s.enabled)
      await refresh()
    } catch (e) {
      setError(errText(e))
    }
  }

  const set = (patch: Partial<MCPDraft>) => setDraft((d) => (d ? { ...d, ...patch } : d))

  return (
    <div className="flex-1 overflow-y-auto px-[22px] py-7">
      <div className="max-w-[720px] mx-auto flex flex-col gap-5">
        <div className="flex items-start justify-between gap-3">
          <div>
            <h2 className="m-0 text-[20px] font-bold tracking-tight">MCP 服务器</h2>
            <p className="mt-1 text-muted text-[13px]">连接外部工具服务器(Model Context Protocol)。与命令行共用同一份 <code className="font-mono text-[12px] bg-surface2 px-1 py-0.5 rounded">config.toml</code>,更改在<b className="text-ink font-semibold">下次新建会话</b>时生效。</p>
          </div>
          {!draft && (
            <button className={`${BTN} ${BTN_PRIMARY} flex-none`} onClick={() => setDraft(draftFrom())}>
              <Icon name="plus" size={15} /> 新建
            </button>
          )}
        </div>

        {error && <div className="text-red text-[13px] bg-redbg border border-red/25 rounded-lg px-3 py-2.5 whitespace-pre-wrap break-words">{error}</div>}

        {draft ? (
          <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-3.5 shadow-xs">
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
          <div className="text-muted text-[13px] py-6 text-center">加载中…</div>
        ) : servers.length === 0 ? (
          <div className="bg-surface border border-line2 border-dashed rounded-[14px] px-5 py-10 text-center">
            <div className="text-muted text-[14px]">还没有配置 MCP 服务器</div>
            <div className="text-faint text-[12.5px] mt-1">点右上角「新建」接入一个外部工具服务器(如文件系统、浏览器、数据库)。</div>
          </div>
        ) : (
          <div className="flex flex-col gap-2.5">
            {servers.map((s) => (
              <div key={s.name} className="bg-surface border border-line2 rounded-[14px] p-4 shadow-xs flex flex-col gap-2">
                <div className="flex items-center gap-2.5">
                  <span className={`w-2 h-2 rounded-full flex-none ${!s.enabled ? 'bg-faint' : s.connected ? 'bg-green' : 'bg-amber'}`} />
                  <span className="font-semibold text-[14.5px]">{s.name}</span>
                  <span className="font-mono text-[10.5px] uppercase tracking-wide text-muted bg-surface2 border border-line2 rounded px-1.5 py-0.5">{s.transport}</span>
                  <span className="text-[12px] text-muted ml-1">
                    {!s.enabled ? '已停用' : s.connected ? `已连接 · ${s.toolCount} 个工具` : '未连接'}
                  </span>
                  <div className="ml-auto flex items-center gap-1.5">
                    <button className={`${BTN} px-2.5 py-1 text-[12.5px]`} onClick={() => toggle(s)}>{s.enabled ? '停用' : '启用'}</button>
                    <button className={`${BTN} px-2.5 py-1 text-[12.5px]`} onClick={() => setDraft(draftFrom(s))}>编辑</button>
                    <button className={`${BTN} ${BTN_DANGER} px-2.5 py-1 text-[12.5px]`} onClick={() => remove(s.name)}>删除</button>
                  </div>
                </div>
                <div className="font-mono text-[12px] text-muted break-all pl-[18px]">
                  {s.transport === 'http' ? s.url : [s.command, ...(s.args ?? [])].filter(Boolean).join(' ')}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
