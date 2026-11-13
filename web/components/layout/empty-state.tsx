import { CloudAlertIcon, RefreshCwIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

/** Dashed "unprinted sheet" for lists with nothing in them yet. */
export function EmptyState({
  icon,
  title,
  children,
  actions,
  className,
  tone = "default",
}: {
  icon?: React.ReactNode
  title: React.ReactNode
  children?: React.ReactNode
  actions?: React.ReactNode
  className?: string
  tone?: "default" | "error"
}) {
  return (
    <div
      className={cn(
        "bg-press flex flex-col items-center gap-3 rounded-2xl border border-dashed border-input/70 px-6 py-12 text-center",
        className
      )}
      data-tone={tone}
    >
      {icon && (
        <div
          className={cn(
            "mb-1 grid size-11 place-items-center rounded-xl bg-card shadow-press ring-1 ring-border [&_svg]:size-5",
            tone === "error" ? "text-destructive" : "text-brand"
          )}
        >
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

/** The one "couldn't load" state: what failed, the server's message, and a retry. */
export function LoadError({
  title,
  message,
  onRetry,
  className,
}: {
  title: string
  message: string
  onRetry: () => void
  className?: string
}) {
  return (
    <div role="alert" className={className}>
      <EmptyState
        tone="error"
        icon={<CloudAlertIcon />}
        title={title}
        actions={
          <Button variant="outline" size="lg" onClick={onRetry}>
            <RefreshCwIcon /> Try again
          </Button>
        }
      >
        <p>{message}</p>
      </EmptyState>
    </div>
  )
}
