"use client"

import { ArrowRightLeftIcon, HourglassIcon } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import { LoadError } from "@/components/layout/empty-state"
import { absoluteTime, relativeTime, useApi, type Site } from "@/components/site/data"
import { IssuedLink } from "@/components/site/members-link"
import { Button } from "@/components/ui/button"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { api, errorMessage } from "@/lib/api"

type Pending = { email: string; createdAt: string; expiresAt: string }
type State = { transfer: Pending | null; mailEnabled: boolean }

/** Ownership transfer, for the owner: start one, see it pending, cancel it. */
export function Transfer({ site }: { site: Site }) {
  const [load, reload] = useApi<State>(`/app-api/sites/${site.id}/transfer`)
  const [email, setEmail] = useState("")
  const [notify, setNotify] = useState(true)
  const [confirming, setConfirming] = useState(false)
  const [canceling, setCanceling] = useState(false)
  const [issued, setIssued] = useState<{ link: string; email: string; emailed: boolean } | null>(null)

  async function start() {
    if (load.state !== "ready") return
    const res = await api<{ transfer: Pending; link: string; emailed: boolean }>(`/app-api/sites/${site.id}/transfer`, {
      method: "POST",
      body: { email, notify: load.data.mailEnabled && notify },
    })
    setIssued({ link: res.link, email: res.transfer.email, emailed: res.emailed })
    setEmail("")
    reload()
  }

  async function cancel() {
    setCanceling(true)
    try {
      await api(`/app-api/sites/${site.id}/transfer`, { method: "DELETE" })
      toast.success("Transfer canceled")
      setIssued(null)
      reload()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setCanceling(false)
    }
  }

  if (load.state === "loading") {
    return <Skeleton className="h-48 w-full rounded-2xl" aria-busy="true" aria-label="Loading transfer status" />
  }
  if (load.state === "error") {
    return <LoadError title="Couldn’t load transfer status" message={load.error.message} onRetry={reload} />
  }
  const { transfer, mailEnabled } = load.data

  return (
    <div className="flex flex-col gap-4">
      <div className="overflow-hidden rounded-2xl bg-card ring-1 ring-destructive/30" data-testid="transfer">
        <div className="flex flex-col gap-1 border-b border-destructive/20 bg-destructive/5 px-5 py-3.5">
          <p className="eyebrow text-destructive">Danger zone</p>
          <p className="text-sm text-muted-foreground">
            The new owner gets full control, including the public link and deleting the site. You stay on as a manager.
          </p>
        </div>
        {transfer && (
          <div className="flex flex-col gap-3 border-b border-border px-5 py-4 sm:flex-row sm:items-center" data-testid="transfer-pending">
            <p className="flex min-w-0 flex-1 items-start gap-2.5 text-sm">
              <HourglassIcon className="mt-0.5 size-4 shrink-0 text-brand-strong" aria-hidden="true" />
              <span className="min-w-0">
                Waiting for <span className="font-medium break-all">{transfer.email}</span> to accept. Offered{" "}
                <time title={absoluteTime(transfer.createdAt)}>{relativeTime(transfer.createdAt)}</time>, expires{" "}
                <time title={absoluteTime(transfer.expiresAt)}>{relativeTime(transfer.expiresAt)}</time>.
              </span>
            </p>
            <Button variant="outline" size="sm" onClick={cancel} disabled={canceling} data-testid="cancel-transfer">
              {canceling && <Spinner />}
              {canceling ? "Canceling…" : "Cancel transfer"}
            </Button>
          </div>
        )}
        <form
          className="flex flex-col gap-3 p-5"
          aria-label="Transfer ownership"
          onSubmit={(e) => {
            e.preventDefault()
            setConfirming(true)
          }}
        >
          <Label htmlFor="transfer-email">{transfer ? "Offer it to someone else instead" : "New owner’s email"}</Label>
          <div className="flex flex-col gap-2 sm:flex-row">
            <Input
              id="transfer-email"
              type="email"
              inputMode="email"
              autoComplete="off"
              placeholder="name@example.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
              className="h-10 sm:flex-1"
            />
            <Button type="submit" variant="destructive" size="lg" className="h-10" disabled={email.trim() === ""} data-testid="start-transfer">
              <ArrowRightLeftIcon /> Transfer ownership
            </Button>
          </div>
          {mailEnabled && (
            <label className="flex w-fit cursor-pointer items-center gap-2.5 text-sm pointer-coarse:min-h-11">
              <input
                type="checkbox"
                checked={notify}
                onChange={(e) => setNotify(e.target.checked)}
                className="size-4 shrink-0 accent-(--brand-strong)"
              />
              Email them the link
            </label>
          )}
        </form>
      </div>
      {issued && (
        <IssuedLink
          testId="transfer-link-panel"
          title={`Transfer link for ${issued.email}`}
          link={issued.link}
          onDismiss={() => setIssued(null)}
        >
          <p>
            {issued.emailed ? "We emailed it to them; you can share it yourself too." : "Send it to them."} Whoever accepts it
            while signed in becomes the owner. Until then nothing changes, and you can cancel it here.
          </p>
        </IssuedLink>
      )}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={`Offer ${site.title} to ${email.trim()}?`}
        description={
          <>
            You’ll get a link for them. When they accept it, they become the owner and you become a manager; you can’t undo
            that yourself.{transfer && ` The pending offer to ${transfer.email} is canceled.`}
          </>
        }
        action="Create transfer link"
        pending="Creating link…"
        testId="confirm-transfer"
        onConfirm={start}
      />
    </div>
  )
}
