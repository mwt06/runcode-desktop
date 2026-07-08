export type PreviewTab = { relPath: string }

// openTab appends relPath as a new tab (focused), or just focuses it if already open.
export function openTab(tabs: PreviewTab[], _active: string | null, relPath: string): { tabs: PreviewTab[]; active: string } {
  if (tabs.some((t) => t.relPath === relPath)) return { tabs, active: relPath }
  return { tabs: [...tabs, { relPath }], active: relPath }
}

// closeTab removes relPath. If it was the active tab, focus moves to the right
// neighbor, else the left, else null (no tabs left).
export function closeTab(tabs: PreviewTab[], active: string | null, relPath: string): { tabs: PreviewTab[]; active: string | null } {
  const idx = tabs.findIndex((t) => t.relPath === relPath)
  if (idx === -1) return { tabs, active }
  const next = tabs.filter((t) => t.relPath !== relPath)
  if (active !== relPath) return { tabs: next, active }
  const neighbor = next[idx] ?? next[idx - 1] ?? null
  return { tabs: next, active: neighbor ? neighbor.relPath : null }
}

// setActive focuses relPath if it is an open tab, otherwise leaves active unchanged.
export function setActive(tabs: PreviewTab[], active: string | null, relPath: string): string | null {
  return tabs.some((t) => t.relPath === relPath) ? relPath : active
}
