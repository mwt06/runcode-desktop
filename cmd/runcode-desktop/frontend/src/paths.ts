// Shared path-display helpers. Workspace paths arrive with either / or \
// separators (Go backend on Windows vs web-style relative paths), so every
// helper normalizes before splitting.

// basename returns the last path segment ('' for empty input).
export const basename = (p?: string): string => (p ? p.replace(/\\/g, '/').split('/').pop() || p : '')

// shortenPath keeps a path chip readable: the parent + leaf segment (e.g.
// "…\runcode_desktop\frontend"), preserving the original separator; show the
// full path on hover via title.
export function shortenPath(p?: string): string {
  if (!p) return ''
  const parts = p.split(/[\\/]+/).filter(Boolean)
  if (parts.length <= 2) return p
  const sep = p.includes('\\') ? '\\' : '/'
  return '…' + sep + parts.slice(-2).join(sep)
}
