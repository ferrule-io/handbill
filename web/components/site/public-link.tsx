"use client"

import { GlobeIcon, GlobeLockIcon, RotateCwIcon } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import { LoadError } from "@/components/layout/empty-state"
import { absoluteTime, can, relativeTime, useApi, type Site } from "@/components/site/data"
import { IssuedLink } from "@/components/site/members-link"
import { Button } from "@/components/ui/button"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
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
  const [enabling, setEnabling] = useState(false)
  const owner = can.managePublicLink(site.role)

  async function run(op: Op) {
    const res = await api<{ enabled: boolean; link: string | null }>(`/app-api/sites/${site.id}/public-link`, {
      method: "POST",
      body: { op },
    })
    setIssued(res.link ? { link: res.link, rotated: op === "rotate" } : null)
    if (op === "disable") toast.success("Public link turned off")
    reload()
  }

  async function enable() {
    setEnabling(true)
    try {
      await run("enable")
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setEnabling(false)
    }
  }

  if (load.state === "loading") {
    return <Skeleton className="h-[5.75rem] w-full rounded-2xl" aria-busy="true" aria-label="Loading the public link" />
  }
  if (load.state === "error") {
    return <LoadError title="Couldn’t load the public link" message={load.error.message} onRetry={reload} />
  }
  const { enabled, updatedAt } = load.data

  return (
    <div className="flex flex-col gap-4" data-testid="public-link" data-enabled={enabled || undefined}>
      <div className="flex flex-col gap-4 rounded-2xl bg-card p-5 shadow-press ring-1 ring-border sm:flex-row sm:items-center">
        <div className="flex min-w-0 flex-1 items-center gap-4">
          <div
            aria-hidden="true"
            className={cn(
              "grid size-11 shrink-0 place-items-center rounded-xl ring-1 [&_svg]:size-5",
              enabled ? "bg-brand-soft text-brand-strong ring-brand/25" : "bg-muted text-muted-foreground ring-border"
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
                "Turn it on to share the site with people who don’t have an account, like a group chat."
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
                <ConfirmDialog
                  trigger={
                    <Button variant="outline" size="lg" data-testid="rotate-public-link">
                      <RotateCwIcon /> Rotate
                    </Button>
                  }
                  title="Replace the public link?"
                  description="The current link stops working immediately, and so does access for everyone who opened it. You’ll get a new link to share."
                  action="Rotate link"
                  pending="Rotating…"
                  testId="confirm-action"
                  onConfirm={() => run("rotate")}
                />
                <ConfirmDialog
                  trigger={
                    <Button variant="outline" size="lg" className="text-destructive hover:text-destructive" data-testid="disable-public-link">
                      Turn off
                    </Button>
                  }
                  title="Turn off the public link?"
                  description="Anyone who isn’t a member loses access on their next page load. Turning it back on issues a new link."
                  action="Turn off"
                  pending="Turning off…"
                  testId="confirm-action"
                  onConfirm={() => run("disable")}
                />
              </>
            ) : (
              <Button variant="brand" size="lg" onClick={enable} disabled={enabling} data-testid="enable-public-link">
                {enabling ? <Spinner /> : <GlobeIcon />}
                {enabling ? "Creating…" : "Create public link"}
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
