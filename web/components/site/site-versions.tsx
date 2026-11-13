"use client"

import { HistoryIcon, RotateCcwIcon } from "lucide-react"
import { toast } from "sonner"

import { EmptyState, LoadError } from "@/components/layout/empty-state"
import {
  absoluteTime,
  formatBytes,
  plural,
  relativeTime,
  useApi,
  type Site,
  type Version,
} from "@/components/site/data"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api"
import { cn } from "@/lib/utils"

/** Version history with rollback, for managers and the owner. */
export function SiteVersions({ site, onRollback }: { site: Site; onRollback: () => void }) {
  const [load, reload] = useApi<{ versions: Version[] }>(`/app-api/sites/${site.id}/versions`)

  if (load.state === "loading") {
    return (
      <div className="divide-y divide-border rounded-2xl bg-card ring-1 ring-border" aria-busy="true" aria-label="Loading versions">
        {[0, 1, 2].map((i) => (
          <div key={i} className="flex items-center gap-4 px-5 py-4">
            <Skeleton className="h-8 w-11 rounded-lg" />
            <div className="flex flex-1 flex-col gap-1.5">
              <Skeleton className="h-4 w-48 max-w-full" />
              <Skeleton className="h-3 w-32 max-w-full" />
            </div>
            <Skeleton className="h-7 w-24" />
          </div>
        ))}
      </div>
    )
  }
  if (load.state === "error") {
    return <LoadError title="Couldn’t load versions" message={load.error.message} onRetry={reload} />
  }
  const versions = load.data.versions
  if (versions.length === 0) {
    return (
      <EmptyState icon={<HistoryIcon />} title="No versions yet">
        <p>Each time an agent publishes, the result is kept here as a version, and any of them can be put back live.</p>
      </EmptyState>
    )
  }
  return (
    <ol className="divide-y divide-border overflow-hidden rounded-2xl bg-card shadow-press ring-1 ring-border">
      {versions.map((v, i) => (
        <VersionRow
          key={v.id}
          site={site}
          version={v}
          number={versions.length - i}
          onRollback={() => {
            reload()
            onRollback()
          }}
        />
      ))}
    </ol>
  )
}

function VersionRow({
  site,
  version,
  number,
  onRollback,
}: {
  site: Site
  version: Version
  number: number
  onRollback: () => void
}) {
  return (
    <li
      className={cn(
        "flex flex-col gap-3 px-5 py-4 sm:flex-row sm:items-center sm:gap-5",
        version.current && "bg-brand-soft/40"
      )}
      data-testid="version-row"
      data-current={version.current || undefined}
    >
      <div className="flex min-w-0 items-center gap-4 sm:contents">
        <span
          className={cn(
            "grid h-8 min-w-11 shrink-0 place-items-center rounded-lg px-2 font-mono text-xs font-medium ring-1",
            version.current ? "bg-brand-strong text-brand-foreground ring-brand-strong" : "bg-background text-muted-foreground ring-border"
          )}
        >
          <span className="sr-only">Version </span>v{number}
        </span>
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <p className="text-sm font-medium">
            <time dateTime={version.createdAt} title={absoluteTime(version.createdAt)}>
              {relativeTime(version.createdAt)}
            </time>
            <span className="font-normal text-muted-foreground">
              {" · "}
              {version.publishedBy ?? "former member"}
            </span>
          </p>
          <p className="truncate font-mono text-xs text-muted-foreground">
            {plural(version.fileCount, "file")} · {formatBytes(version.size)} · {version.id.slice(-8)}
          </p>
        </div>
      </div>
      <div className="flex shrink-0 justify-end">
        {version.current ? (
          <Badge className="bg-brand-strong text-brand-foreground">Live</Badge>
        ) : (
          <ConfirmDialog
            trigger={
              <Button variant="outline" size="sm" data-testid="rollback-button">
                <RotateCcwIcon /> Roll back
              </Button>
            }
            tone="brand"
            title={`Make version ${number} live?`}
            description={
              <>
                Everyone viewing {site.title} sees the version published {relativeTime(version.createdAt)} on their next
                page load. Newer versions stay in the history, so you can switch back.
              </>
            }
            action={`Roll back to v${number}`}
            pending="Rolling back…"
            testId="confirm-rollback"
            onConfirm={async () => {
              await api(`/app-api/sites/${site.id}/rollback`, { method: "POST", body: { versionId: version.id } })
              toast.success(`Version ${number} is live`)
              onRollback()
            }}
          />
        )}
      </div>
    </li>
  )
}
