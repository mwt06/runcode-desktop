// Shared compact formatting for token counts and durations (usage footers,
// context meter, sub-agent cards).

// fmtTokens renders a token count compactly: 340 → "340", 1234 → "1.2k", 23000 → "23k".
export function fmtTokens(n: number): string {
  if (n >= 10000) return Math.round(n / 1000) + 'k'
  if (n >= 1000) return (n / 1000).toFixed(1) + 'k'
  return String(n)
}

// fmtDuration renders elapsed ms compactly: 850 → "0.8s", 3200 → "3.2s", 75000 → "1m15s".
// (0.85 的二进制表示略小于 0.85，toFixed(1) 因此给出 0.8——注释按实际行为写。)
export function fmtDuration(ms?: number): string {
  if (!ms || ms < 0) return ''
  const s = ms / 1000
  if (s < 60) return (s < 10 ? s.toFixed(1) : Math.round(s).toString()) + 's'
  const m = Math.floor(s / 60)
  return `${m}m${Math.round(s % 60)}s`
}

// fmtBytes 把字节数渲染成人读得懂的大小：0 → ""，84213760 → "80.3 MB"。
//
// 空串而不是 "0 B"：调用它的地方（更新下载的进度）拿到 0 的含义是「服务端没给
// 长度」，画一个 "0 B" 会让人以为要下的是个空文件。二进制进位（1024）是因为这些
// 数要和 Windows 资源管理器显示的大小对得上——用 1000 进位会差出百分之几，而用户
// 会拿这两个数互相对照。
export function fmtBytes(n?: number): string {
  if (!n || n <= 0) return ''
  const units = ['B', 'KB', 'MB', 'GB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return (i === 0 ? v : v.toFixed(1)) + ' ' + units[i]
}
