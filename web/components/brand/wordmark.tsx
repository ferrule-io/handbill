import { useId } from "react"

import { WORDMARK_PATH } from "@/components/brand/wordmark-path"
import { cn } from "@/lib/utils"

/** A document-unique mask id; the collapsed header renders two marks. */
function useMaskId() {
  return `hb-mark-${useId().replace(/[^\w-]/g, "")}`
}

/** docs/brand/svg/mark.svg geometry with ink and vermilion swapped for theme tokens. */
function MarkArt({ maskId }: { maskId: string }) {
  return (
    <>
      <defs>
        <mask id={maskId} maskUnits="userSpaceOnUse" x="-400" y="-400" width="800" height="800">
          <rect x="-400" y="-400" width="800" height="800" fill="#fff" />
          <g
            transform="translate(6 -131.0) rotate(28) scale(1.0)"
            fill="#000"
            stroke="#000"
            strokeWidth="21.0"
            strokeLinejoin="round"
            strokeLinecap="round"
          >
            <ellipse cx="0" cy="-40" rx="25" ry="11" />
            <path d="M-10,-46 L-7.5,-70 H7.5 L10,-46 Z" />
            <ellipse cx="0" cy="-78" rx="19" ry="12" />
            <path d="M0,-32 V-3" fill="none" strokeWidth="26.25" />
          </g>
        </mask>
      </defs>
      <g transform="rotate(-7)">
        <g
          mask={`url(#${maskId})`}
          fill="none"
          className="stroke-foreground"
          strokeWidth="7.0"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <path
            d="M-127.5,84 V-151.0 Q-127.5,-165.0 -113.5,-165.0 H113.5 Q127.5,-165.0 127.5,-151.0 V151.0 Q127.5,165.0 113.5,165.0 H-26 C-88,165.0 -127.5,136 -127.5,84 Z"
            fill="none"
          />
          <path d="M-127.5,84 C-105.5,80 -85.5,82 -79.5,100 C-71.5,128 -57.5,157.0 -26,165.0" />
          <path d="M-127.5,-127.0 H127.5" />
          <path d="M-107.5,-146.0 h12" />
          <path d="M-81.5,-146.0 h12" />
          <path d="M-55.5,-146.0 h12" />
          <path d="M-101.5,-103.0 h203.0 v30 h-203.0 Z" />
          <path d="M-101.5,-51.0 h84 v84 h-84 Z M-101.5,-51.0 l84,84 M-17.5,-51.0 l-84,84" />
          <path d="M2.5,-43.0 H101.5" />
          <path d="M2.5,-20.0 H101.5" />
          <path d="M2.5,3.0 H101.5" />
          <path d="M2.5,26.0 H57.5" />
          <path d="M-101.5,67.0 H101.5" />
          <path d="M-49.5,91.0 H101.5" />
          <path d="M-35.5,115.0 H101.5" />
          <path d="M-23.5,139.0 H57.5" />
        </g>
        <g transform="translate(6 -131.0) rotate(28) scale(1.0)" className="fill-brand">
          <ellipse cx="0" cy="-40" rx="25" ry="11" />
          <path d="M-10,-46 L-7.5,-70 H7.5 L10,-46 Z" />
          <ellipse cx="0" cy="-78" rx="19" ry="12" />
          <path d="M0,-32 V-3" fill="none" className="stroke-brand" strokeWidth="5.25" strokeLinecap="round" />
        </g>
      </g>
    </>
  )
}

/** The Handbill mark (docs/brand/svg/mark.svg) in theme colours; 48 px tall, the brand minimum. */
export function Mark({ className }: { className?: string }) {
  return (
    <svg viewBox="-166.63 -234.68 333.27 424.49" aria-hidden="true" className={cn("h-12 w-auto shrink-0", className)}>
      <MarkArt maskId={useMaskId()} />
    </svg>
  )
}

/**
 * The horizontal lockup (docs/brand/svg/lockup-horizontal.svg) in theme
 * colours; `collapse` shows only the mark below the sm breakpoint.
 */
export function Wordmark({ className, collapse = false }: { className?: string; collapse?: boolean }) {
  return (
    <span className={cn("inline-flex shrink-0", className)}>
      {collapse && <Mark className="sm:hidden" />}
      <svg
        viewBox="-166.63 -234.68 1041.48 424.49"
        role="img"
        aria-label="Handbill"
        className={cn("h-12 w-auto", collapse && "hidden sm:block")}
      >
        <MarkArt maskId={useMaskId()} />
        <path transform="translate(190.98 41.41) scale(0.08737)" d={WORDMARK_PATH} className="fill-foreground" />
      </svg>
    </span>
  )
}
