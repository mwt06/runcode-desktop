// Tiny status glyphs used wherever a step reports done / in-flight / failed: the
// plan timeline, tool rows, sub-agent rows, system-event dividers. Kept apart from
// Icon (a name→path lookup) because these are drawn, not looked up.

// CheckMark is the small tick used by the completed marker and the done footer.
export function CheckMark({ size = 9, className }: { size?: number; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={3.2} strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden>
      <path d="M5 12l4.5 4.5L19 7" />
    </svg>
  )
}

// WarnTriangle marks a warning/failure on the system-event dividers. Drawn rather
// than reusing Icon's "shield", which carries a permissions meaning here.
export function WarnTriangle({ size = 12, className }: { size?: number; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.2} strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden>
      <path d="M12 3.5 1.8 20.5h20.4z" />
      <path d="M12 9.5v4.6" />
      <path d="M12 17.4h.01" />
    </svg>
  )
}

// Spinner is a small ring that rotates while a step is in flight (used by the
// progress pill and running tool rows).
export function Spinner({ size = 14 }: { size?: number }) {
  return (
    <span
      className="spin-ring inline-block flex-none rounded-full border-2 border-[var(--color-primarysoft)] border-t-[var(--color-primary)]"
      style={{ width: size, height: size }}
    />
  )
}
