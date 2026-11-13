import { cn } from "@/lib/utils"

/** Dashed "unprinted sheet" for lists with nothing in them yet. */
export function EmptyState({
  icon,
  title,
  children,
  actions,
  className,
}: {
  icon?: React.ReactNode
  title: React.ReactNode
  children?: React.ReactNode
  actions?: React.ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        "bg-press flex flex-col items-center gap-3 rounded-2xl border border-dashed border-input px-6 py-14 text-center",
        className
      )}
    >
      {icon && (
        <div className="mb-1 grid size-11 place-items-center rounded-xl bg-card text-brand shadow-press ring-1 ring-border [&_svg]:size-5">
          {icon}
        </div>
      )}
      <h3 className="text-xl font-medium">{title}</h3>
      {children && (
        <div className="max-w-md text-sm leading-relaxed text-muted-foreground">{children}</div>
      )}
      {actions && <div className="mt-3 flex flex-wrap justify-center gap-2">{actions}</div>}
    </div>
  )
}
