// A preview tab is either a workspace file (rendered in PreviewPanel) or an edit
// review (rendered in DiffPanel). Tabs are keyed by tabKey: the relPath for files,
// "diff:<snapshotId>" for reviews, so a file and its diff coexist as two tabs.
export type PreviewTab =
  | { kind: 'file'; relPath: string }
  | { kind: 'diff'; snapshotId: string; relPath: string }

export function tabKey(t: PreviewTab): string {
  return t.kind === 'diff' ? 'diff:' + t.snapshotId : t.relPath
}

// openTab appends tab (focused), or just focuses it if a tab with the same key is
// already open.
export function openTab(tabs: PreviewTab[], _active: string | null, tab: PreviewTab): { tabs: PreviewTab[]; active: string } {
  const key = tabKey(tab)
  if (tabs.some((t) => tabKey(t) === key)) return { tabs, active: key }
  return { tabs: [...tabs, tab], active: key }
}

// closeTab removes the tab with the given key. If it was active, focus moves to the
// right neighbor, else the left, else null.
export function closeTab(tabs: PreviewTab[], active: string | null, key: string): { tabs: PreviewTab[]; active: string | null } {
  const idx = tabs.findIndex((t) => tabKey(t) === key)
  if (idx === -1) return { tabs, active }
  const next = tabs.filter((t) => tabKey(t) !== key)
  if (active !== key) return { tabs: next, active }
  const neighbor = next[idx] ?? next[idx - 1] ?? null
  return { tabs: next, active: neighbor ? tabKey(neighbor) : null }
}

// setActive focuses the tab with key if open, otherwise leaves active unchanged.
export function setActive(tabs: PreviewTab[], active: string | null, key: string): string | null {
  return tabs.some((t) => tabKey(t) === key) ? key : active
}
