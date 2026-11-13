import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"

/** One item in a settings section: icon tile, title, detail, and an action. */
export function Row({
  icon,
  title,
  badge,
  detail,
  action,
  tone,
}: {
  icon: React.ReactNode
  title: React.ReactNode
  badge?: React.ReactNode
  detail?: React.ReactNode
  action?: React.ReactNode
  tone?: "warn"
}) {
  return (
    <div className="flex flex-col gap-4 rounded-2xl bg-card p-4 ring-1 ring-border sm:flex-row sm:items-center sm:p-5">
      <div className="flex min-w-0 flex-1 items-center gap-4">
        <div
          className={cn(
            "grid size-10 shrink-0 place-items-center rounded-xl bg-muted text-foreground [&_svg]:size-4.5",
            tone === "warn" && "bg-brand-soft text-brand"
          )}
        >
          {icon}
        </div>
        <div className="flex min-w-0 flex-col gap-0.5">
          <div className="flex flex-wrap items-center gap-2 text-sm font-medium">
            {title}
            {badge}
          </div>
          {detail && <p className="text-sm text-muted-foreground">{detail}</p>}
        </div>
      </div>
      {action && <div className="shrink-0 max-sm:pl-14 sm:ml-auto">{action}</div>}
    </div>
  )
}

export function RowSkeleton() {
  return (
    <div className="flex items-center gap-4 rounded-2xl bg-card p-5 ring-1 ring-border">
      <Skeleton className="size-10 rounded-xl" />
      <div className="flex flex-1 flex-col gap-2">
        <Skeleton className="h-4 w-40" />
        <Skeleton className="h-3.5 w-64 max-w-full" />
      </div>
    </div>
  )
}

const dateFmt = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", year: "numeric" })

export function formatDate(iso: string) {
  return dateFmt.format(new Date(iso))
}

export function relative(iso: string) {
  const seconds = (new Date(iso).getTime() - Date.now()) / 1000
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" })
  const abs = Math.abs(seconds)
  if (abs < 60) return "just now"
  if (abs < 3600) return rtf.format(Math.round(seconds / 60), "minute")
  if (abs < 86400) return rtf.format(Math.round(seconds / 3600), "hour")
  return rtf.format(Math.round(seconds / 86400), "day")
}
