// Shared compact formatting for token counts and durations (usage footers,
// context meter, sub-agent cards).

// fmtTokens renders a token count compactly: 340 → "340", 1234 → "1.2k", 23000 → "23k".
export function fmtTokens(n: number): string {
  if (n >= 10000) return Math.round(n / 1000) + 'k'
  if (n >= 1000) return (n / 1000).toFixed(1) + 'k'
  return String(n)
}

// fmtDuration renders elapsed ms compactly: 850 → "0.9s", 3200 → "3.2s", 75000 → "1m15s".
export function fmtDuration(ms?: number): string {
  if (!ms || ms < 0) return ''
  const s = ms / 1000
  if (s < 60) return (s < 10 ? s.toFixed(1) : Math.round(s).toString()) + 's'
  const m = Math.floor(s / 60)
  return `${m}m${Math.round(s % 60)}s`
}
