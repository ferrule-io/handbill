import { cn } from "@/lib/utils"

/** The agent-dash mark: an ink plate holding a dog-eared page and a vermilion dash. */
export function Mark({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 32 32"
      aria-hidden="true"
      className={cn("size-7 shrink-0", className)}
    >
      <rect width="32" height="32" rx="8" className="fill-foreground" />
      <path d="M10 7.5h8.25L23 12.25V24.5H10Z" className="fill-background" />
      <path d="M18.25 7.5v4.75H23Z" className="fill-foreground opacity-25" />
      <rect x="12.5" y="17.75" width="7.5" height="2.75" rx="1.375" className="fill-brand" />
    </svg>
  )
}

/**
 * Mark plus the "agent—dash" logotype set in the display serif. `collapse`
 * hides the logotype below the sm breakpoint (crowded mobile headers).
 */
export function Wordmark({ className, collapse = false }: { className?: string; collapse?: boolean }) {
  return (
    <span className={cn("inline-flex items-center gap-2.5", className)}>
      <Mark />
      <span
        className={cn(
          "font-heading text-[1.15rem] font-semibold tracking-tight whitespace-nowrap [font-variation-settings:'SOFT'_100,'WONK'_1]",
          collapse && "sr-only sm:not-sr-only"
        )}
      >
        agent<span className="text-brand">—</span>dash
      </span>
    </span>
  )
}
