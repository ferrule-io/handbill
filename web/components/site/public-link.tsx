"use client"

import { GlobeIcon, GlobeLockIcon, RefreshCwIcon, RotateCwIcon } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import { EmptyState } from "@/components/layout/empty-state"
import { absoluteTime, can, relativeTime, useApi, type Site } from "@/components/site/data"
import { IssuedLink } from "@/components/site/members-link"
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
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { api, errorMessage } from "@/lib/api"
import { cn } from "@/lib/utils"

type State = { enabled: boolean; updatedAt: string | null }
type Op = "enable" | "disable" | "rotate"

/**
 * "Anyone with the link" access. Everyone who manages the site sees whether
 * it is on; only the owner turns it on, off, or rotates it. The link itself
 * is shown once, right after it is issued.
 */
export function PublicLink({ site }: { site: Site }) {
  const [load, reload] = useApi<State>(`/app-api/sites/${site.id}/public-link`)
  const [issued, setIssued] = useState<{ link: string; rotated: boolean } | null>(null)
  const [busy, setBusy] = useState<Op | null>(null)
  const owner = can.managePublicLink(site.role)

  async function run(op: Op) {
    setBusy(op)
    try {
      const res = await api<{ enabled: boolean; link: string | null }>(`/app-api/sites/${site.id}/public-link`, {
        method: "POST",
        body: { op },
      })
      setIssued(res.link ? { link: res.link, rotated: op === "rotate" } : null)
      if (op === "disable") toast.success("Public link turned off")
      reload()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  if (load.state === "loading") return <Skeleton className="h-32 w-full rounded-2xl" />
  if (load.state === "error") {
    return (
      <EmptyState
        title="Couldn’t load the public link"
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
  const { enabled, updatedAt } = load.data

  return (
    <div className="flex flex-col gap-4" data-testid="public-link" data-enabled={enabled || undefined}>
      <div className="flex flex-col gap-4 rounded-2xl bg-card p-5 shadow-press ring-1 ring-border sm:flex-row sm:items-center">
        <div className="flex min-w-0 flex-1 items-center gap-4">
          <div
            className={cn(
              "grid size-11 shrink-0 place-items-center rounded-xl ring-1 [&_svg]:size-5",
              enabled ? "bg-brand-soft text-brand ring-brand/25" : "bg-muted text-muted-foreground ring-border"
            )}
          >
            {enabled ? <GlobeIcon /> : <GlobeLockIcon />}
          </div>
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <p className="font-medium" data-testid="public-link-state">
              {enabled ? "On: anyone with the link can view" : "Off: only members can view"}
            </p>
            <p className="text-sm text-muted-foreground">
              {enabled && updatedAt ? (
                <>
                  Link issued <time title={absoluteTime(updatedAt)}>{relativeTime(updatedAt)}</time>. Visitors don’t need
                  an account.
                </>
              ) : owner ? (
                "Turn it on to share the site without accounts, e.g. with a group."
              ) : (
                "The owner can turn on a link that works without an account."
              )}
            </p>
          </div>
        </div>
        {owner && (
          <div className="flex shrink-0 flex-wrap gap-2">
            {enabled ? (
              <>
                <Confirm
                  trigger={
                    <Button variant="outline" disabled={busy !== null} data-testid="rotate-public-link">
                      <RotateCwIcon /> {busy === "rotate" ? "Rotating…" : "Rotate"}
                    </Button>
                  }
                  title="Replace the public link?"
                  description="The current link stops working immediately, and so does access for everyone who opened it. You’ll get a new link to share."
                  action="Rotate link"
                  onConfirm={() => run("rotate")}
                />
                <Confirm
                  trigger={
                    <Button variant="ghost" disabled={busy !== null} data-testid="disable-public-link">
                      {busy === "disable" ? "Turning off…" : "Turn off"}
                    </Button>
                  }
                  title="Turn off the public link?"
                  description="Anyone who isn’t a member loses access on their next page load. Turning it back on issues a new link."
                  action="Turn off"
                  destructive
                  onConfirm={() => run("disable")}
                />
              </>
            ) : (
              <Button variant="brand" onClick={() => run("enable")} disabled={busy !== null} data-testid="enable-public-link">
                <GlobeIcon /> {busy === "enable" ? "Creating…" : "Create public link"}
              </Button>
            )}
          </div>
        )}
      </div>
      {issued && (
        <IssuedLink
          testId="public-link-panel"
          title={issued.rotated ? "New public link" : "Public link created"}
          link={issued.link}
          onDismiss={() => setIssued(null)}
        >
          <p>
            Anyone who opens it can view {site.title} without signing in.
            {issued.rotated && " The previous link no longer works."} Copy it now. To share it again later, rotate it for a
            new one.
          </p>
        </IssuedLink>
      )}
    </div>
  )
}

function Confirm({
  trigger,
  title,
  description,
  action,
  destructive,
  onConfirm,
}: {
  trigger: React.ReactNode
  title: string
  description: string
  action: string
  destructive?: boolean
  onConfirm: () => void
}) {
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant={destructive ? "destructive" : "brand"} onClick={onConfirm} data-testid="confirm-action">
            {action}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
