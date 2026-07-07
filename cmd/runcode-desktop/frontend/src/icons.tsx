import { useId } from 'react'

// Inline SVG icons (stroke = currentColor) so they always render in WebView2 —
// no icon font or network needed. Line style to match the product look.

type Props = { name: string; size?: number; className?: string }

// Logo is the XRUN mark: an "X" of two crossing strokes, blue and violet. Used
// for the app brand and the assistant avatar.
export function Logo({ size = 24 }: { size?: number }) {
  const a = useId()
  const b = useId()
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" aria-hidden>
      <defs>
        <linearGradient id={a} x1="5" y1="5" x2="19" y2="19" gradientUnits="userSpaceOnUse">
          <stop stopColor="#8b5cf6" />
          <stop offset="1" stopColor="#6366f1" />
        </linearGradient>
        <linearGradient id={b} x1="19" y1="5" x2="5" y2="19" gradientUnits="userSpaceOnUse">
          <stop stopColor="#3b82f6" />
          <stop offset="1" stopColor="#22d3ee" />
        </linearGradient>
      </defs>
      <path d="M6.6 6.6 17.4 17.4" stroke={`url(#${a})`} strokeWidth="3.6" strokeLinecap="round" />
      <path d="M17.4 6.6 6.6 17.4" stroke={`url(#${b})`} strokeWidth="3.6" strokeLinecap="round" />
    </svg>
  )
}

const stroke = {
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.8,
  strokeLinecap: 'round' as const,
  strokeLinejoin: 'round' as const,
}

export function Icon({ name, size = 18, className }: Props) {
  const common = {
    width: size,
    height: size,
    viewBox: '0 0 24 24',
    className,
    'aria-hidden': true,
  }

  switch (name) {
    case 'home':
      return (
        <svg {...common} {...stroke}>
          <path d="M3 10.8 12 3l9 7.8" />
          <path d="M5.5 9.6V20h13V9.6" />
        </svg>
      )
    case 'chat':
      return (
        <svg {...common} {...stroke}>
          <path d="M4 6a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2H9l-4 3.2V8a2 2 0 0 1-1-1Z" />
        </svg>
      )
    case 'compass':
      return (
        <svg {...common} {...stroke}>
          <circle cx="12" cy="12" r="9" />
          <path d="m15.5 8.5-2.2 4.8-4.8 2.2 2.2-4.8z" />
        </svg>
      )
    case 'grid':
      return (
        <svg {...common} {...stroke}>
          <rect x="3.5" y="3.5" width="7" height="7" rx="1.6" />
          <rect x="13.5" y="3.5" width="7" height="7" rx="1.6" />
          <rect x="3.5" y="13.5" width="7" height="7" rx="1.6" />
          <rect x="13.5" y="13.5" width="7" height="7" rx="1.6" />
        </svg>
      )
    case 'book':
      return (
        <svg {...common} {...stroke}>
          <path d="M5 4.5h10.5a2 2 0 0 1 2 2V20H7a2 2 0 0 1-2-2Z" />
          <path d="M5 17.5a2 2 0 0 1 2-2h10.5" />
        </svg>
      )
    case 'settings':
      return (
        <svg {...common} {...stroke}>
          <path d="M4 7h9" />
          <path d="M17 7h3" />
          <circle cx="15" cy="7" r="2.1" />
          <path d="M4 17h3" />
          <path d="M11 17h9" />
          <circle cx="9" cy="17" r="2.1" />
        </svg>
      )
    case 'plus':
      return (
        <svg {...common} {...stroke}>
          <path d="M12 5v14M5 12h14" />
        </svg>
      )
    case 'bot':
      return (
        <svg {...common} {...stroke}>
          <rect x="4.5" y="8" width="15" height="11" rx="2.5" />
          <path d="M12 4.5V8M9.5 13h.01M14.5 13h.01M9 16.5h6" />
          <path d="M4.5 11.5H3M21 11.5h-1.5" />
        </svg>
      )
    case 'terminal':
      return (
        <svg {...common} {...stroke}>
          <rect x="3.3" y="5" width="17.4" height="14" rx="2.2" />
          <path d="M7 9.5l3 2.5-3 2.5M12.5 15h4.5" />
        </svg>
      )
    case 'pencil':
      return (
        <svg {...common} {...stroke}>
          <path d="M4 20.5l1-4L16 5.5a1.8 1.8 0 0 1 2.5 0l.5.5a1.8 1.8 0 0 1 0 2.5L8 19.5z" />
          <path d="M14.5 7.5l2.5 2.5" />
        </svg>
      )
    case 'search':
      return (
        <svg {...common} {...stroke}>
          <circle cx="11" cy="11" r="6.2" />
          <path d="M20 20l-4.4-4.4" />
        </svg>
      )
    case 'globe':
      return (
        <svg {...common} {...stroke}>
          <circle cx="12" cy="12" r="8.5" />
          <path d="M3.6 12h16.8M12 3.5c2.6 2.6 2.6 14.4 0 17M12 3.5c-2.6 2.6-2.6 14.4 0 17" />
        </svg>
      )
    case 'plug':
      return (
        <svg {...common} {...stroke}>
          <path d="M9 3v4.5M15 3v4.5" />
          <path d="M6.8 7.5h10.4V10a5.2 5.2 0 0 1-10.4 0V7.5Z" />
          <path d="M12 15.2V21" />
        </svg>
      )
    case 'hash':
      return (
        <svg {...common} {...stroke}>
          <path d="M9 4 7.5 20M16.5 4 15 20M4.5 9h15M3.8 15h15" />
        </svg>
      )
    case 'folder':
      return (
        <svg {...common} {...stroke}>
          <path d="M3.5 6.5a1.5 1.5 0 0 1 1.5-1.5h4l2 2h7a1.5 1.5 0 0 1 1.5 1.5v8a1.5 1.5 0 0 1-1.5 1.5H5a1.5 1.5 0 0 1-1.5-1.5z" />
        </svg>
      )
    case 'trash':
      return (
        <svg {...common} {...stroke}>
          <path d="M4 7h16" />
          <path d="M9.5 7V5.6a1.1 1.1 0 0 1 1.1-1.1h2.8a1.1 1.1 0 0 1 1.1 1.1V7" />
          <path d="M6.6 7l.9 12.4a1 1 0 0 0 1 .95h7a1 1 0 0 0 1-.95L17.4 7" />
        </svg>
      )
    case 'file':
      return (
        <svg {...common} {...stroke}>
          <path d="M7 3.5h7l4 4V19.5a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1v-15a1 1 0 0 1 1-1Z" />
          <path d="M13.7 3.5V8h4.3" />
        </svg>
      )
    case 'clock':
      return (
        <svg {...common} {...stroke}>
          <circle cx="12" cy="12" r="9" />
          <path d="M12 7.5V12l3 1.8" />
        </svg>
      )
    case 'more':
      return (
        <svg {...common} viewBox="0 0 24 24" width={size} height={size} aria-hidden fill="currentColor">
          <circle cx="5" cy="12" r="1.6" />
          <circle cx="12" cy="12" r="1.6" />
          <circle cx="19" cy="12" r="1.6" />
        </svg>
      )
    case 'sparkles':
      return (
        <svg {...common} {...stroke}>
          <path d="M12 3.5 13.7 8 18 9.7 13.7 11.4 12 16l-1.7-4.6L6 9.7l4.3-1.7z" />
          <path d="M18.5 14.5l.8 1.9 1.9.8-1.9.8-.8 1.9-.8-1.9-1.9-.8 1.9-.8z" />
        </svg>
      )
    case 'chevron-down':
      return (
        <svg {...common} {...stroke}>
          <path d="m6 9.5 6 6 6-6" />
        </svg>
      )
    case 'compress':
      return (
        <svg {...common} {...stroke}>
          <path d="M9 9 4 4m5 1.2V9H5.2" />
          <path d="m15 9 5-5m-5 1.2V9h3.8" />
          <path d="m9 15-5 5m5-1.2V15H5.2" />
          <path d="m15 15 5 5m-5-1.2V15h3.8" />
        </svg>
      )
    case 'at':
      return (
        <svg {...common} {...stroke}>
          <circle cx="12" cy="12" r="3.6" />
          <path d="M15.6 8.6v4.6a2.4 2.4 0 0 0 4.8 0V12a8.4 8.4 0 1 0-3.4 6.8" />
        </svg>
      )
    case 'shield':
      return (
        <svg {...common} {...stroke}>
          <path d="M12 3.5 5.5 6v5c0 4.4 2.9 7.4 6.5 8.8 3.6-1.4 6.5-4.4 6.5-8.8V6z" />
          <path d="m9.3 12 1.9 1.9 3.5-3.8" />
        </svg>
      )
    case 'paperclip':
      return (
        <svg {...common} {...stroke}>
          <path d="M19 11.4 11.6 18.8a4 4 0 0 1-5.7-5.7l7.2-7.2a2.6 2.6 0 0 1 3.7 3.7l-7.2 7.2a1.2 1.2 0 0 1-1.7-1.7l6.5-6.5" />
        </svg>
      )
    case 'send':
      return (
        <svg {...common} {...stroke}>
          <path d="M21 3 3 10.5l6.5 2.4L12 19.5z" />
          <path d="m9.5 12.9 11.5-9.9" />
        </svg>
      )
    case 'stop':
      return (
        <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden fill="currentColor">
          <rect x="6.5" y="6.5" width="11" height="11" rx="2.5" />
        </svg>
      )
    case 'bell':
      return (
        <svg {...common} {...stroke}>
          <path d="M6 9.5a6 6 0 0 1 12 0c0 4.5 2 5.5 2 5.5H4s2-1 2-5.5Z" />
          <path d="M10 18.5a2 2 0 0 0 4 0" />
        </svg>
      )
    case 'win-min':
      return (
        <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden fill="none" stroke="currentColor" strokeWidth={1.4} strokeLinecap="round">
          <path d="M5 12h14" />
        </svg>
      )
    case 'win-max':
      return (
        <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden fill="none" stroke="currentColor" strokeWidth={1.4}>
          <rect x="6" y="6" width="12" height="12" rx="1.5" />
        </svg>
      )
    case 'win-close':
      return (
        <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round">
          <path d="M6.5 6.5l11 11M17.5 6.5l-11 11" />
        </svg>
      )
    default:
      return null
  }
}
