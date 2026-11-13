import { cn } from "@/lib/utils"

/**
 * Optional icon tile, eyebrow, serif headline, and lede at the top of
 * every auth-shell screen (sign-in, invites, consent, verification).
 */
export function AuthHeading({
  icon,
  tone = "brand",
  eyebrow,
  title,
  children,
  titleTestId,
}: {
  icon?: React.ReactNode
  tone?: "brand" | "muted" | "error"
  eyebrow: React.ReactNode
  title: React.ReactNode
  children?: React.ReactNode
  titleTestId?: string
}) {
  return (
    <div className="flex flex-col gap-2.5">
      {icon && (
        <div
          aria-hidden="true"
          className={cn(
            "mb-2 grid size-11 place-items-center rounded-xl ring-1 [&_svg]:size-5",
            tone === "brand" && "bg-brand-soft text-brand-strong ring-brand/20",
            tone === "muted" && "bg-muted text-foreground ring-border",
            tone === "error" && "bg-destructive/10 text-destructive ring-destructive/20"
          )}
        >
          {icon}
        </div>
      )}
      <p className="eyebrow">{eyebrow}</p>
      <h1 className="text-[2rem] leading-[1.08] font-medium tracking-[-0.015em] text-balance" data-testid={titleTestId}>
        {title}
      </h1>
      {children && <div className="flex flex-col gap-2 text-sm leading-relaxed text-muted-foreground">{children}</div>}
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
