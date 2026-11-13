import { cn } from "@/lib/utils"

/**
 * Top of every app page: eyebrow label, serif title, optional lede and
 * right-aligned actions, closed by the brand-ticked rule.
 */
export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
  className,
}: {
  eyebrow?: React.ReactNode
  title: React.ReactNode
  description?: React.ReactNode
  actions?: React.ReactNode
  className?: string
}) {
  return (
    <header className={cn("flex flex-col gap-6 pt-10 sm:pt-14", className)}>
      <div className="flex flex-col gap-5 sm:flex-row sm:items-end sm:justify-between">
        <div className="flex min-w-0 flex-col gap-3">
          {eyebrow && <p className="eyebrow">{eyebrow}</p>}
          <h1 className="text-title font-medium">{title}</h1>
          {description && (
            <p className="max-w-xl text-[0.95rem] leading-relaxed text-muted-foreground">
              {description}
            </p>
          )}
        </div>
        {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
      </div>
      <div className="rule-tick" />
    </header>
  )
}

/** A titled block within a page. */
export function Section({
  title,
  description,
  actions,
  children,
  className,
}: {
  title: React.ReactNode
  description?: React.ReactNode
  actions?: React.ReactNode
  children: React.ReactNode
  className?: string
}) {
  return (
    <section className={cn("grid gap-5 md:grid-cols-[14rem_1fr] md:gap-10", className)}>
      <div className="flex flex-col gap-1.5">
        <h2 className="text-lg font-medium">{title}</h2>
        {description && (
          <p className="text-sm leading-relaxed text-muted-foreground">{description}</p>
        )}
        {actions && <div className="mt-2">{actions}</div>}
      </div>
      <div className="min-w-0">{children}</div>
    </section>
  )
}
