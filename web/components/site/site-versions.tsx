"use client"

import { HistoryIcon, RefreshCwIcon, RotateCcwIcon } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import { EmptyState } from "@/components/layout/empty-state"
import {
  absoluteTime,
  formatBytes,
  plural,
  relativeTime,
  useApi,
  type Site,
  type Version,
} from "@/components/site/data"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { api, ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"

/** Version history with rollback, for managers and the owner. */
export function SiteVersions({ site, onRollback }: { site: Site; onRollback: () => void }) {
  const [load, reload] = useApi<{ versions: Version[] }>(`/app-api/sites/${site.id}/versions`)

  if (load.state === "loading") {
    return (
      <div className="divide-y divide-border rounded-2xl bg-card ring-1 ring-border" aria-busy="true">
        {[0, 1, 2].map((i) => (
          <div key={i} className="flex items-center gap-4 px-5 py-4">
            <Skeleton className="h-6 w-10" />
            <Skeleton className="h-4 w-48" />
            <Skeleton className="ml-auto h-7 w-20" />
          </div>
        ))}
      </div>
    )
  }
  if (load.state === "error") {
    return (
      <EmptyState
        title="Couldn’t load versions"
        actions={
          <Button variant="outline" onClick={reload}>
            <RefreshCwIcon /> Try again
          </Button>
        }
      >
        <p>{load.error.message}</p>
      </EmptyState>
    )
  }
  const versions = load.data.versions
  if (versions.length === 0) {
    return (
      <EmptyState icon={<HistoryIcon />} title="No versions yet">
        <p>Each publish from an agent becomes a version here, and any of them can be restored.</p>
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
      <div className="flex items-center gap-4 sm:contents">
        <span
          className={cn(
            "grid h-8 min-w-11 place-items-center rounded-lg px-2 font-mono text-xs font-medium ring-1",
            version.current ? "bg-brand text-brand-foreground ring-brand" : "bg-background text-muted-foreground ring-border"
          )}
          aria-label={`Version ${number}`}
        >
          v{number}
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
          <p className="font-mono text-xs text-muted-foreground">
            {plural(version.fileCount, "file")} · {formatBytes(version.size)} · {version.id.slice(-8)}
          </p>
        </div>
      </div>
      <div className="flex shrink-0 justify-end">
        {version.current ? (
          <Badge variant="default" className="bg-brand text-brand-foreground">
            Live
          </Badge>
        ) : (
          <RollbackButton site={site} version={version} number={number} onRollback={onRollback} />
        )}
      </div>
    </li>
  )
}

function RollbackButton({
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
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  async function rollback(e: React.MouseEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await api(`/app-api/sites/${site.id}/rollback`, { method: "POST", body: { versionId: version.id } })
      toast.success(`Version ${number} is live`)
      setOpen(false)
      onRollback()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Rollback failed.")
    } finally {
      setBusy(false)
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger asChild>
        <Button variant="outline" size="sm" data-testid="rollback-button">
          <RotateCcwIcon /> Roll back
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Make version {number} live?</AlertDialogTitle>
          <AlertDialogDescription>
            Everyone viewing {site.title} sees the version published {relativeTime(version.createdAt)} on
            their next page load. Newer versions stay in the history, so you can switch back.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="brand" onClick={rollback} disabled={busy} data-testid="confirm-rollback">
            {busy ? "Rolling back…" : `Roll back to v${number}`}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
