// 租户树：AI.Core 的租户带 parentId 层级，渲染成缩进树，只有末级(叶子)可点选。
import { type ReactNode } from 'react'
import { type PassportTenant } from '@/core/bridge'

export type TenantNode = { t: PassportTenant; children: TenantNode[] }

// buildTenantTree 把扁平租户列表按 parentId 组装成森林。指向自身、指向不存在的
// 父节点都按根处理，避免坏数据造成断链或自引用死循环。纯函数。
export function buildTenantTree(tenants: PassportTenant[]): TenantNode[] {
  const byId = new Map(tenants.map((t) => [t.id, t]))
  const kids = new Map<string, PassportTenant[]>()
  const roots: PassportTenant[] = []
  for (const t of tenants) {
    const pid = t.parentId
    if (pid && pid !== t.id && byId.has(pid)) {
      const arr = kids.get(pid) ?? []
      arr.push(t)
      kids.set(pid, arr)
    } else roots.push(t)
  }
  const build = (t: PassportTenant): TenantNode => ({ t, children: (kids.get(t.id) ?? []).map(build) })
  return roots.map(build)
}

export function TenantTree({ nodes, selectableIds, selectedId, disabled, onSelect, depth = 0 }: {
  nodes: TenantNode[]
  selectableIds: Set<string>
  selectedId: string
  disabled: boolean
  onSelect: (id: string) => void
  depth?: number
}) {
  return (
    <>
      {nodes.flatMap((n): ReactNode[] => {
        const pad = { paddingLeft: `${8 + depth * 16}px` }
        const leaf = selectableIds.has(n.t.id)
        const row = leaf ? (
          <button
            key={n.t.id}
            type="button"
            disabled={disabled}
            onClick={() => onSelect(n.t.id)}
            style={pad}
            className={`flex items-center gap-2 w-full text-left pr-2 py-1.5 rounded-[7px] text-[13px] transition-colors disabled:opacity-60 ${selectedId === n.t.id ? 'bg-primarysoft text-primary font-medium' : 'hover:bg-surface2 text-ink'}`}
          >
            <span className={`w-[7px] h-[7px] rounded-full flex-none ${selectedId === n.t.id ? 'bg-primary' : 'bg-line2'}`} />
            <span className="truncate">{n.t.name}</span>
          </button>
        ) : (
          <div key={n.t.id} style={pad} className="pr-2 py-1.5 text-[11.5px] text-faint font-medium">{n.t.name}</div>
        )
        return [
          row,
          <TenantTree
            key={n.t.id + ':children'}
            nodes={n.children}
            selectableIds={selectableIds}
            selectedId={selectedId}
            disabled={disabled}
            onSelect={onSelect}
            depth={depth + 1}
          />,
        ]
      })}
    </>
  )
}
