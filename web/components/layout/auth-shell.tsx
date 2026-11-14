import Link from "next/link"

import { Wordmark } from "@/components/brand/wordmark"
import { ThemeToggle } from "@/components/theme/theme-toggle"

/**
 * Two-panel layout for sign-up, sign-in, and invite acceptance: an ink
 * "proof sheet" panel with the product voice, and the form column.
 */
export function AuthShell({
  aside,
  children,
}: {
  /** Headline for the ink panel, shown on large screens only. */
  aside: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="grid min-h-svh lg:grid-cols-[minmax(0,1.05fr)_minmax(0,1fr)]">
      <aside className="dark relative hidden overflow-hidden border-r border-border bg-card text-foreground lg:flex lg:flex-col">
        <div className="bg-press absolute inset-0 opacity-70" aria-hidden="true" />
        <div
          aria-hidden="true"
          className="absolute -right-24 -bottom-24 size-[28rem] rounded-full bg-brand/25 blur-3xl"
        />
        <div className="relative flex flex-1 flex-col justify-between p-12 xl:p-16">
          <Link href="/" className="w-fit rounded-md">
            <Wordmark />
          </Link>
          <div className="flex max-w-lg flex-col gap-6">
            <p className="eyebrow text-brand">Proof · No. 001</p>
            <div className="font-heading text-display font-medium [font-variation-settings:'SOFT'_100,'WONK'_1] [&_em]:text-brand [&_em]:italic">
              {aside}
            </div>
          </div>
          <p className="font-mono text-xs text-muted-foreground">
            Static sites from your agents, behind your own front door.
          </p>
        </div>
      </aside>
      <div className="relative flex flex-col">
        <div className="flex items-center justify-between p-5 sm:p-8">
          <Link href="/" className="flex items-center rounded-md pointer-coarse:min-h-11 lg:invisible">
            <Wordmark />
          </Link>
          <ThemeToggle />
        </div>
        <div className="flex flex-1 items-center justify-center px-5 pb-16 sm:px-8">
          <div className="w-full max-w-sm">{children}</div>
        </div>
      </div>
    </div>
  )
}
