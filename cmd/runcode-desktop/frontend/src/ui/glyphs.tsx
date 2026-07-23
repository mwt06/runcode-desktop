// Two tiny status glyphs used wherever a step reports done / in-flight: the plan
// timeline, tool rows, sub-agent rows. Kept apart from Icon (a name→path lookup)
// because both are drawn, not looked up.

// CheckMark is the small tick used by the completed marker and the done footer.
export function CheckMark({ size = 9, className }: { size?: number; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={3.2} strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden>
      <path d="M5 12l4.5 4.5L19 7" />
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
