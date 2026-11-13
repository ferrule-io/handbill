"use client"

import { CrownIcon, LinkIcon, MailOpenIcon, TimerOffIcon } from "lucide-react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useCallback, useEffect, useRef, useState } from "react"

import { Spinner } from "@/components/auth/parts"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { api, ApiError, errorMessage, useMe } from "@/lib/api"
import { fragmentParam, useHash } from "@/lib/params"

/**
 * /invite#token=<token>: accepts an invite or an ownership transfer
 * (docs/design.md#sharing). The token stays in the fragment, so it never
 * reaches server logs; it is kept in sessionStorage across the sign-up or
 * sign-in round trip (which returns to /invite without it), the invite is
 * accepted automatically on return, and the stored token is then cleared.
 */

const TOKEN_KEY = "handbill.invite.token"
// Set when the visitor leaves to sign up or sign in from this page, so the
// return accepts without another click.
const AUTO_KEY = "handbill.invite.auto"

type Lookup = {
  kind: "invite" | "transfer"
  status: "pending" | "accepted" | "expired" | "revoked"
  acceptedByYou: boolean
  site: { id: string; title: string } | null
  from?: string | null
  email?: string
  role?: "owner" | "manager" | "viewer"
  expiresAt?: string
}

type View =
  | { state: "loading" }
  | { state: "unknown"; token: string }
  | { state: "error"; token: string; message: string }
  | { state: "ready"; token: string; lookup: Lookup }

function storage(): Storage | null {
  try {
    return window.sessionStorage
  } catch {
    return null
  }
}

function forget() {
  storage()?.removeItem(TOKEN_KEY)
  storage()?.removeItem(AUTO_KEY)
}

export function InviteAccept() {
  const hash = useHash()
  const { user, loading: meLoading } = useMe()
  const [view, setView] = useState<View>({ state: "loading" })

  // The token: from the fragment, else the one kept across sign-in.
  const token = hash === null ? undefined : (fragmentParam(hash, "token") ?? storage()?.getItem(TOKEN_KEY) ?? null)
  const signedIn = user?.id ?? null

  // Re-checked when the signed-in account changes (what the lookup reveals
  // depends on it).
  useEffect(() => {
    if (!token || meLoading) return
    storage()?.setItem(TOKEN_KEY, token)
    const ctl = new AbortController()
    api<Lookup>("/app-api/invites/lookup", { method: "POST", body: { token }, signal: ctl.signal })
      .then((lookup) => setView({ state: "ready", token, lookup }))
      .catch((err: unknown) => {
        if (ctl.signal.aborted) return
        if (err instanceof ApiError && err.status === 404) {
          forget()
          setView({ state: "unknown", token })
        } else {
          setView({ state: "error", token, message: errorMessage(err) })
        }
      })
    return () => ctl.abort()
  }, [token, meLoading, signedIn])

  if (token === null || token === "") return <MissingView />
  if (view.state === "loading") return <LoadingView />
  if (view.state === "unknown") {
    return (
      <Notice icon={<LinkIcon />} title="This link isn’t valid" token={view.token}>
        Check that you opened the whole link from the message you received. If it still doesn’t work, ask the person who
        sent it for a new one.
      </Notice>
    )
  }
  if (view.state === "error") {
    return (
      <Notice icon={<LinkIcon />} title="Couldn’t check this link" token={view.token}>
        {view.message}
      </Notice>
    )
  }
  return <Ready token={view.token} lookup={view.lookup} />
}

function Ready({ token, lookup }: { token: string; lookup: Lookup }) {
  const router = useRouter()
  const { user } = useMe()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const started = useRef(false)
  const transfer = lookup.kind === "transfer"

  const accept = useCallback(async () => {
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ siteId: string }>("/app-api/invites/accept", { method: "POST", body: { token } })
      forget()
      router.push(`/site?id=${res.siteId}&welcome=1`)
    } catch (err) {
      storage()?.removeItem(AUTO_KEY)
      setError(errorMessage(err))
      setBusy(false)
    }
  }, [router, token])

  // Back from sign-up or sign-in: accept without another click. A link that
  // can no longer be accepted is dropped from storage, except a used one
  // seen signed out: it may be this visitor's (auto-granted to their
  // verified address), which signing in reveals.
  useEffect(() => {
    if (lookup.status !== "pending") {
      if (user || lookup.status !== "accepted") forget()
      return
    }
    if (!user || started.current || storage()?.getItem(AUTO_KEY) !== "1") return
    started.current = true
    void accept()
  }, [user, lookup.status, accept])

  if (lookup.status === "accepted" && lookup.acceptedByYou && lookup.site) {
    return (
      <Offer lookup={lookup} eyebrow="Already accepted" title={lookup.site.title}>
        <p className="text-sm leading-relaxed text-muted-foreground">
          {transfer ? "You own this site." : "This invite is on your account."} Open it from its page.
        </p>
        <Button asChild variant="brand" size="xl">
          <Link href={`/site?id=${lookup.site.id}&welcome=1`} data-testid="go-to-site">
            Go to the site
          </Link>
        </Button>
      </Offer>
    )
  }
  if (lookup.status === "accepted" && !user) {
    return (
      <Notice
        icon={<MailOpenIcon />}
        title="Sign in to continue"
        token={token}
        action={
          <Button asChild variant="brand" size="xl">
            <Link href={`/login?return=${encodeURIComponent("/invite")}`} onClick={() => storage()?.setItem(AUTO_KEY, "1")}>
              Sign in
            </Link>
          </Button>
        }
      >
        This link has been used. If it was for you, your account already has access: sign in to open the site.
      </Notice>
    )
  }
  if (lookup.status !== "pending" || !lookup.site) {
    const title = {
      accepted: "This link was already used",
      expired: "This link has expired",
      revoked: "This link was withdrawn",
      pending: "This link isn’t valid",
    }[lookup.status]
    return (
      <Notice icon={<TimerOffIcon />} title={title} token={token}>
        {transfer ? "Ownership links" : "Invite links"} work once, for a limited time. Ask the person who sent it for a new
        one.
      </Notice>
    )
  }

  const returnTo = encodeURIComponent("/invite")
  const leave = () => storage()?.setItem(AUTO_KEY, "1")
  return (
    <Offer
      lookup={lookup}
      eyebrow={transfer ? "Ownership transfer" : "Invitation"}
      title={lookup.site.title}
    >
      {user ? (
        <div className="flex flex-col gap-3">
          <p className="text-sm leading-relaxed text-muted-foreground">
            Signed in as <span className="font-medium text-foreground">{user.email || "your account"}</span>.{" "}
            {lookup.email && user.email && lookup.email !== user.email
              ? `It was sent to ${lookup.email}, and will go to the account you’re signed in with.`
              : transfer
                ? "Accepting makes this account the owner."
                : "Accepting adds the site to this account."}
          </p>
          <Button variant="brand" size="xl" onClick={accept} disabled={busy} data-testid="accept-invite">
            {busy && <Spinner />}
            {transfer ? (busy ? "Taking ownership…" : "Accept ownership") : busy ? "Accepting…" : "Accept invitation"}
          </Button>
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          <Button asChild variant="brand" size="xl">
            <Link href={`/signup?return=${returnTo}`} onClick={leave} data-testid="invite-signup">
              Create an account
            </Link>
          </Button>
          <Button asChild variant="outline" size="xl">
            <Link href={`/login?return=${returnTo}`} onClick={leave} data-testid="invite-login">
              I already have an account
            </Link>
          </Button>
          <p className="text-center text-xs text-muted-foreground">
            No password: you’ll sign in with a passkey on this device.
          </p>
        </div>
      )}
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <TokenTail token={token} />
    </Offer>
  )
}

const roleText: Record<string, string> = {
  viewer: "as a viewer",
  manager: "as a manager: you can publish and invite viewers",
  owner: "as its owner",
}

function Offer({
  lookup,
  eyebrow,
  title,
  children,
}: {
  lookup: Lookup
  eyebrow: string
  title: string
  children: React.ReactNode
}) {
  const transfer = lookup.kind === "transfer"
  const from = lookup.from || "Someone"
  return (
    <div className="flex flex-col gap-8" data-testid="invite-ready" data-kind={lookup.kind}>
      <div className="flex flex-col gap-3">
        <div className="grid size-11 place-items-center rounded-xl bg-brand-soft text-brand [&_svg]:size-5">
          {transfer ? <CrownIcon /> : <MailOpenIcon />}
        </div>
        <p className="eyebrow">{eyebrow}</p>
        <h1 className="text-[2rem] leading-tight font-medium text-balance" data-testid="invite-site-title">
          {title}
        </h1>
        <p className="text-sm leading-relaxed text-muted-foreground" data-testid="invite-summary">
          {transfer ? (
            <>
              <span className="font-medium text-foreground">{from}</span> is offering you ownership of this site. They stay
              on as a manager.
            </>
          ) : (
            <>
              <span className="font-medium text-foreground">{from}</span> invited you to this site{" "}
              {roleText[lookup.role ?? "viewer"]}.
            </>
          )}
        </p>
      </div>
      {children}
    </div>
  )
}

function TokenTail({ token }: { token: string }) {
  return (
    <div className="flex items-center justify-between rounded-xl bg-muted px-4 py-3 font-mono text-xs text-muted-foreground">
      <span>link</span>
      <span className="text-foreground" data-testid="invite-token-tail">
        …{token.slice(-6)}
      </span>
    </div>
  )
}

function Notice({
  icon,
  title,
  token,
  action,
  children,
}: {
  icon: React.ReactNode
  title: string
  token: string
  action?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-6" data-testid="invite-unusable">
      <div className="flex flex-col gap-4">
        <div className="grid size-11 place-items-center rounded-xl bg-muted [&_svg]:size-5">{icon}</div>
        <h1 className="text-[2rem] leading-tight font-medium">{title}</h1>
        <p className="text-sm leading-relaxed text-muted-foreground">{children}</p>
      </div>
      <TokenTail token={token} />
      {action ?? (
        <Button asChild variant="outline" size="xl">
          <Link href="/">Go to handbill</Link>
        </Button>
      )}
    </div>
  )
}

function MissingView() {
  return (
    <div className="flex flex-col gap-4" data-testid="invite-missing">
      <div className="grid size-11 place-items-center rounded-xl bg-muted [&_svg]:size-5">
        <LinkIcon />
      </div>
      <h1 className="text-[2rem] leading-tight font-medium">This invite link is incomplete</h1>
      <p className="text-sm leading-relaxed text-muted-foreground">
        Open the full link from the message you received. It ends with{" "}
        <code className="font-mono text-foreground">#token=…</code>
      </p>
    </div>
  )
}

function LoadingView() {
  return (
    <div className="flex flex-col gap-4" aria-busy="true">
      <Skeleton className="size-11 rounded-xl" />
      <Skeleton className="h-3 w-20" />
      <Skeleton className="h-9 w-64" />
      <Skeleton className="h-4 w-full" />
      <Skeleton className="mt-4 h-12 w-full rounded-xl" />
    </div>
  )
}
