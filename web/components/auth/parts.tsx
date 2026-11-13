import { CircleAlertIcon, LoaderCircleIcon } from "lucide-react"

import { cn } from "@/lib/utils"

/** Eyebrow, serif headline, and lede at the top of every auth screen. */
export function AuthHeading({
  eyebrow,
  title,
  children,
}: {
  eyebrow: React.ReactNode
  title: React.ReactNode
  children?: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-2.5">
      <p className="eyebrow">{eyebrow}</p>
      <h1 className="text-[2rem] leading-[1.08] font-medium tracking-[-0.015em]">{title}</h1>
      {children && <div className="text-sm leading-relaxed text-muted-foreground">{children}</div>}
    </div>
  )
}

/** Inline, announced error under a form. */
export function FormError({ children, className }: { children?: React.ReactNode; className?: string }) {
  if (!children) return null
  return (
    <div
      role="alert"
      className={cn(
        "flex gap-2.5 rounded-lg border border-destructive/25 bg-destructive/8 px-3 py-2.5 text-sm leading-snug text-destructive dark:bg-destructive/12",
        className
      )}
    >
      <CircleAlertIcon className="mt-px size-4 shrink-0" aria-hidden="true" />
      <div>{children}</div>
    </div>
  )
}

/** Hairline "or" between the passkey and Google options. */
export function OrDivider() {
  return (
    <div className="flex items-center gap-3 font-mono text-[0.7rem] tracking-widest text-muted-foreground uppercase" aria-hidden="true">
      <span className="h-px flex-1 bg-border" />
      or
      <span className="h-px flex-1 bg-border" />
    </div>
  )
}

export function Spinner({ className, ...props }: React.ComponentProps<typeof LoaderCircleIcon>) {
  return <LoaderCircleIcon className={cn("animate-spin", className)} aria-hidden="true" {...props} />
}

/** Small print under the primary passkey action. */
export function PasskeyNote({ children }: { children: React.ReactNode }) {
  return (
    <p className="flex items-start gap-2 font-mono text-[0.7rem] leading-relaxed text-muted-foreground">
      <span className="mt-[0.45rem] size-1 shrink-0 rounded-full bg-brand" aria-hidden="true" />
      <span>{children}</span>
    </p>
  )
}

/** Footer link line ("New here? Create an account"). */
export function AuthFooterLink({ children }: { children: React.ReactNode }) {
  return (
    <p className="text-sm text-muted-foreground [&_a]:font-medium [&_a]:text-foreground [&_a]:underline [&_a]:decoration-brand [&_a]:decoration-2 [&_a]:underline-offset-4">
      {children}
    </p>
  )
}
