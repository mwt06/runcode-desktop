// 草稿「已点开」本机记忆：未点开显示紫点。纯 localStorage，换机不共享。

const KEY = 'mail.draft.seen.v1'

function loadMap(): Record<string, true> {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return {}
    const parsed = JSON.parse(raw) as Record<string, unknown>
    if (!parsed || typeof parsed !== 'object') return {}
    const out: Record<string, true> = {}
    for (const id of Object.keys(parsed)) {
      if (parsed[id]) out[id] = true
    }
    return out
  } catch {
    return {}
  }
}

function saveMap(map: Record<string, true>): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(map))
  } catch {
    // 配额失败不挡编辑。
  }
}

export function isDraftSeen(draftId: string): boolean {
  if (!draftId) return true
  return !!loadMap()[draftId]
}

export function markDraftSeen(draftId: string): void {
  if (!draftId) return
  const map = loadMap()
  if (map[draftId]) return
  map[draftId] = true
  saveMap(map)
}

export function clearDraftSeen(draftId: string): void {
  if (!draftId) return
  const map = loadMap()
  if (!map[draftId]) return
  delete map[draftId]
  saveMap(map)
}
