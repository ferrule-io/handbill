"use client"

import { ChevronDownIcon, ClockIcon, MailIcon, RotateCwIcon, ShieldIcon, UserMinusIcon, XIcon } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import { LoadError } from "@/components/layout/empty-state"
import { absoluteTime, can, relativeTime, roleLabel, useApi, type Role, type Site } from "@/components/site/data"
import { InviteForm } from "@/components/site/members-invite"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { api, errorMessage } from "@/lib/api"
import { cn } from "@/lib/utils"

/** Shapes of /app-api/sites/{id}/members (internal/sharing/api.go). */
export type Member = { userId: string; email: string | null; role: Role; you: boolean; since: string }
export type PendingInvite = {
  id: string
  email: string
  role: Role
  invitedBy: string | null
  createdAt: string
  expiresAt: string
}
type MembersResponse = { members: Member[]; invites: PendingInvite[]; mailEnabled: boolean }

/** Members, pending invites, and the invite form, for managers and the owner. */
export function SiteMembers({ site }: { site: Site }) {
  const [load, reload] = useApi<MembersResponse>(`/app-api/sites/${site.id}/members`)

  return (
    <div className="flex flex-col gap-6">
      <InviteForm site={site} mailEnabled={load.state === "ready" && load.data.mailEnabled} onInvited={reload} />
      {load.state === "loading" && <ListSkeleton />}
      {load.state === "error" && <LoadError title="Couldn’t load members" message={load.error.message} onRetry={reload} />}
      {load.state === "ready" && (
        <>
          <ul
            className="divide-y divide-border overflow-hidden rounded-2xl bg-card shadow-press ring-1 ring-border"
            aria-label="Members"
            data-testid="member-list"
          >
            {load.data.members.map((m) => (
              <MemberRow key={m.userId} site={site} member={m} onChange={reload} />
            ))}
          </ul>
          {load.data.invites.length > 0 && (
            <div className="flex flex-col gap-3">
              <h3 className="eyebrow" id="pending-invites">
                Pending invites
              </h3>
              <ul
                className="divide-y divide-border overflow-hidden rounded-2xl bg-card ring-1 ring-border"
                aria-labelledby="pending-invites"
                data-testid="invite-list"
              >
                {load.data.invites.map((inv) => (
                  <InviteRow key={inv.id} site={site} invite={inv} mailEnabled={load.data.mailEnabled} onChange={reload} />
                ))}
              </ul>
            </div>
          )}
        </>
      )}
    </div>
  )
}

function ListSkeleton() {
  return (
    <div className="divide-y divide-border rounded-2xl bg-card ring-1 ring-border" aria-busy="true" aria-label="Loading members">
      {[0, 1, 2].map((i) => (
        <div key={i} className="flex items-center gap-3 px-5 py-3.5">
          <Skeleton className="size-9 rounded-full" />
          <div className="flex flex-1 flex-col gap-1.5">
            <Skeleton className="h-4 w-48 max-w-full" />
            <Skeleton className="h-3 w-28" />
          </div>
          <Skeleton className="h-7 w-20" />
        </div>
      ))}
    </div>
  )
}

function Avatar({ label, pending }: { label: string; pending?: boolean }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "grid size-9 shrink-0 place-items-center rounded-full font-heading text-sm font-medium uppercase ring-1",
        pending
          ? "border border-dashed border-input bg-background text-muted-foreground ring-0"
          : "bg-brand-soft text-brand-strong ring-brand/20"
      )}
    >
      {pending ? <MailIcon className="size-4" /> : label.slice(0, 1)}
    </span>
  )
}

function RoleBadge({ role }: { role: Role }) {
  return (
    <Badge
      variant={role === "owner" ? "default" : "outline"}
      className={cn("shrink-0", role === "owner" && "bg-ink text-paper dark:bg-paper dark:text-ink")}
    >
      {role === "owner" && <ShieldIcon />}
      {roleLabel[role]}
    </Badge>
  )
}

function MemberRow({ site, member, onChange }: { site: Site; member: Member; onChange: () => void }) {
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  const name = member.email ?? "Account without an email"
  const canRemove =
    member.role !== "owner" &&
    !member.you &&
    (member.role === "viewer" ? can.manageViewers(site.role) : can.manageManagers(site.role))
  const canChangeRole = member.role !== "owner" && can.manageManagers(site.role)

  async function setRole(role: string) {
    if (role === member.role) return
    setBusy(true)
    try {
      await api(`/app-api/sites/${site.id}/members/${member.userId}`, { method: "PATCH", body: { role } })
      toast.success(`${name} is now a ${role}`)
      onChange()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <li className="flex items-center gap-3 px-5 py-3.5" data-testid="member-row" data-email={member.email ?? undefined}>
      <Avatar label={name} />
      <div className="flex min-w-0 flex-1 flex-col">
        <p className="flex min-w-0 items-center gap-2 text-sm font-medium">
          <span className="truncate">{name}</span>
          {member.you && <span className="shrink-0 text-xs font-normal text-muted-foreground">(you)</span>}
        </p>
        <p className="text-xs text-muted-foreground">
          {member.role === "owner" ? "Owner" : "Member"} since{" "}
          <time title={absoluteTime(member.since)}>{relativeTime(member.since)}</time>
        </p>
      </div>
      {canChangeRole || canRemove ? (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" size="sm" disabled={busy} aria-label={`Manage ${name}, ${roleLabel[member.role]}`} data-testid="member-menu">
              {busy && <Spinner />}
              {roleLabel[member.role]} <ChevronDownIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-60">
            {canChangeRole && (
              <>
                <DropdownMenuLabel>Role</DropdownMenuLabel>
                <DropdownMenuRadioGroup value={member.role} onValueChange={setRole}>
                  <DropdownMenuRadioItem value="viewer">
                    <span className="flex flex-col">
                      Viewer<span className="text-xs text-muted-foreground">Can open the site</span>
                    </span>
                  </DropdownMenuRadioItem>
                  <DropdownMenuRadioItem value="manager">
                    <span className="flex flex-col">
                      Manager<span className="text-xs text-muted-foreground">Publishes and invites viewers</span>
                    </span>
                  </DropdownMenuRadioItem>
                </DropdownMenuRadioGroup>
              </>
            )}
            {canChangeRole && canRemove && <DropdownMenuSeparator />}
            {canRemove && (
              <DropdownMenuItem variant="destructive" onSelect={() => setConfirming(true)}>
                <UserMinusIcon /> Remove access
              </DropdownMenuItem>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      ) : (
        <RoleBadge role={member.role} />
      )}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={`Remove ${name}?`}
        description={`They lose access to ${site.title} on their next page load, including any tab they have open. You can invite them again later.`}
        action="Remove access"
        pending="Removing…"
        testId="confirm-remove"
        onConfirm={async () => {
          await api(`/app-api/sites/${site.id}/members/${member.userId}`, { method: "DELETE" })
          toast.success(`Removed ${name}`)
          onChange()
        }}
      />
    </li>
  )
}

function InviteRow({
  site,
  invite,
  mailEnabled,
  onChange,
}: {
  site: Site
  invite: PendingInvite
  mailEnabled: boolean
  onChange: () => void
}) {
  const [busy, setBusy] = useState<"revoke" | "resend" | null>(null)
  const canRevoke = invite.role === "viewer" ? can.manageViewers(site.role) : can.manageManagers(site.role)

  async function revoke() {
    setBusy("revoke")
    try {
      await api(`/app-api/sites/${site.id}/members/${invite.id}`, { method: "DELETE" })
      toast.success(`Invite for ${invite.email} revoked`)
      onChange()
    } catch (err) {
      toast.error(errorMessage(err))
      setBusy(null)
    }
  }

  // Re-inviting the same address replaces the pending invite with a fresh
  // token and expiry (Service.Invite), so the old link stops working.
  async function resend() {
    setBusy("resend")
    try {
      const res = await api<{ link: string; emailed: boolean; emailNotSentReason: string | null }>(
        `/app-api/sites/${site.id}/members`,
        { method: "POST", body: { email: invite.email, role: invite.role, notify: mailEnabled } },
      )
      if (res.emailed) {
        toast.success(`Invite re-sent to ${invite.email}`)
      } else {
        const reason = res.emailNotSentReason ?? "Email is off; send them the link yourself."
        const copied = await navigator.clipboard.writeText(res.link).then(
          () => true,
          () => false,
        )
        if (copied) {
          toast.success(`New invite link for ${invite.email} copied`, { description: reason })
        } else {
          // The old link is already dead; keep the new one on screen until dismissed.
          toast.warning(`New invite link for ${invite.email}`, {
            description: `${reason} Copy it now: ${res.link}`,
            duration: Infinity,
            closeButton: true,
          })
        }
      }
      onChange()
    } catch (err) {
      toast.error(errorMessage(err))
      setBusy(null)
    }
  }

  return (
    <li className="flex items-center gap-3 px-5 py-3" data-testid="invite-row" data-email={invite.email}>
      <Avatar label={invite.email} pending />
      <div className="flex min-w-0 flex-1 flex-col">
        <p className="truncate text-sm">{invite.email}</p>
        <p className="flex min-w-0 items-center gap-1 text-xs text-muted-foreground">
          <ClockIcon className="size-3 shrink-0" aria-hidden="true" />
          <span className="truncate">
            {roleLabel[invite.role]} · expires{" "}
            <time title={absoluteTime(invite.expiresAt)}>{relativeTime(invite.expiresAt)}</time>
            {invite.invitedBy && <span className="hidden sm:inline"> · from {invite.invitedBy}</span>}
          </span>
        </p>
      </div>
      {canRevoke && (
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="sm"
            onClick={resend}
            disabled={busy !== null}
            aria-label={`Resend invite to ${invite.email}`}
            data-testid="resend-invite"
          >
            {busy === "resend" ? <Spinner /> : <RotateCwIcon />}
            {busy === "resend" ? "Resending…" : "Resend"}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={revoke}
            disabled={busy !== null}
            aria-label={`Revoke invite for ${invite.email}`}
            data-testid="revoke-invite"
          >
            {busy === "revoke" ? <Spinner /> : <XIcon />}
            {busy === "revoke" ? "Revoking…" : "Revoke"}
          </Button>
        </div>
      )}
    </li>
  )
}
