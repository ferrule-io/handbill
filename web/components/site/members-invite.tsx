"use client"

import { SendIcon } from "lucide-react"
import { useState } from "react"

import { absoluteTime, roleLabel, type Role, type Site } from "@/components/site/data"
import { IssuedLink } from "@/components/site/members-link"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { api, errorMessage } from "@/lib/api"
import { cn } from "@/lib/utils"

type InviteResponse = {
  invite: { id: string; email: string; role: Role; expiresAt: string }
  link: string
  granted: boolean
  emailed: boolean
}

const roleHelp: Record<"viewer" | "manager", string> = {
  viewer: "Can open the site.",
  manager: "Can also publish, roll back, and invite viewers.",
}

/** Invite by email: role picker, optional email notification, and the link. */
export function InviteForm({
  site,
  mailEnabled,
  onInvited,
}: {
  site: Site
  mailEnabled: boolean
  onInvited: () => void
}) {
  const roles: ("viewer" | "manager")[] = site.role === "owner" ? ["viewer", "manager"] : ["viewer"]
  const [email, setEmail] = useState("")
  const [role, setRole] = useState<"viewer" | "manager">("viewer")
  const [notify, setNotify] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [issued, setIssued] = useState<InviteResponse | null>(null)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const res = await api<InviteResponse>(`/app-api/sites/${site.id}/members`, {
        method: "POST",
        body: { email, role, notify: mailEnabled && notify },
      })
      setIssued(res)
      setEmail("")
      onInvited()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <form
        onSubmit={submit}
        aria-label="Invite someone"
        className="flex flex-col gap-4 rounded-2xl bg-card p-5 shadow-press ring-1 ring-border"
      >
        <div className="flex flex-col gap-2">
          <Label htmlFor="invite-email">Email address</Label>
          <div className="flex flex-col gap-2 sm:flex-row">
            <Input
              id="invite-email"
              type="email"
              inputMode="email"
              autoComplete="off"
              placeholder="name@example.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
              className="h-10 flex-1"
              aria-invalid={error ? true : undefined}
              aria-describedby={error ? "invite-error" : undefined}
            />
            <Button type="submit" variant="brand" size="lg" className="h-10" disabled={busy || email.trim() === ""} data-testid="send-invite">
              <SendIcon /> {busy ? "Inviting…" : "Invite"}
            </Button>
          </div>
        </div>
        {roles.length > 1 ? (
          <fieldset className="flex flex-col gap-2">
            <legend className="mb-2 text-sm font-medium">Role</legend>
            <div className="grid gap-2 sm:grid-cols-2">
              {roles.map((r) => (
                <label
                  key={r}
                  className={cn(
                    "flex cursor-pointer items-start gap-3 rounded-xl px-3.5 py-3 ring-1 transition-colors",
                    role === r ? "bg-brand-soft/50 ring-brand/50" : "ring-border hover:bg-muted/60"
                  )}
                >
                  <input
                    type="radio"
                    name="invite-role"
                    value={r}
                    checked={role === r}
                    onChange={() => setRole(r)}
                    className="mt-1 size-4 shrink-0 accent-(--brand)"
                    data-testid={`invite-role-${r}`}
                  />
                  <span className="flex flex-col gap-0.5">
                    <span className="text-sm font-medium">{roleLabel[r]}</span>
                    <span className="text-xs leading-snug text-muted-foreground">{roleHelp[r]}</span>
                  </span>
                </label>
              ))}
            </div>
          </fieldset>
        ) : (
          <p className="text-xs text-muted-foreground">
            They’ll join as a <span className="font-medium text-foreground">viewer</span>. Only the owner can add managers.
          </p>
        )}
        {mailEnabled && (
          <label className="flex cursor-pointer items-center gap-2.5 text-sm">
            <input
              type="checkbox"
              checked={notify}
              onChange={(e) => setNotify(e.target.checked)}
              className="size-4 shrink-0 accent-(--brand)"
              data-testid="invite-notify"
            />
            Email them the link
          </label>
        )}
        {error && (
          <p id="invite-error" role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
      </form>
      {issued && (
        <IssuedLink
          testId="invite-link-panel"
          title={issued.granted ? `${issued.invite.email} has access now` : `Invite link for ${issued.invite.email}`}
          link={issued.link}
          onDismiss={() => setIssued(null)}
        >
          {issued.granted ? (
            <p>
              They already have an account with this address, so they can open the site as a{" "}
              {issued.invite.role} right away. The link takes them straight to it.
            </p>
          ) : (
            <p>
              {issued.emailed ? "We emailed it to them; you can share it yourself too." : "Send it to them however you like."}{" "}
              Whoever opens it first and signs in joins as a {issued.invite.role}. It works once, until{" "}
              {absoluteTime(issued.invite.expiresAt)}.
            </p>
          )}
        </IssuedLink>
      )}
    </div>
  )
}
