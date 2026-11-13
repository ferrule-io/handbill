"use client"

import { CrownIcon, LinkIcon, MailOpenIcon, TimerOffIcon, UserRoundCheckIcon } from "lucide-react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useCallback, useEffect, useRef, useState } from "react"

import { AuthHeading } from "@/components/auth/parts"
import { Button } from "@/components/ui/button"
import { FormError } from "@/components/ui/form-error"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
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
  const [attempt, setAttempt] = useState(0)

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
  }, [token, meLoading, signedIn, attempt])

  if (token === null || token === "") return <MissingView />
  if (view.state === "loading") return <LoadingView />
  if (view.state === "unknown") {
    return (
      <Notice icon={<LinkIcon />} title="This link doesn’t work" token={view.token}>
        <p>
          It may have been copied only partly. Open the whole link from the message you received, or ask the person who
          sent it for a new one.
        </p>
      </Notice>
    )
  }
  if (view.state === "error") {
    return (
      <Notice
        icon={<LinkIcon />}
        title="We couldn’t check this link"
        token={view.token}
        action={
          <Button
            variant="outline"
            size="xl"
            onClick={() => {
              setView({ state: "loading" })
              setAttempt((n) => n + 1)
            }}
          >
            Try again
          </Button>
        }
      >
        <p>{view.message}</p>
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
      <Offer lookup={lookup} icon={<UserRoundCheckIcon />} eyebrow="Already yours" title={lookup.site.title} token={token}>
        <p className="text-sm leading-relaxed text-muted-foreground">
          {transfer ? "You already own this site." : "You already accepted this invitation, so the site is on your account."}
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
        tone="brand"
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
        <p>This link has already been used. If it was meant for you, your account already has access: sign in to see the site.</p>
      </Notice>
    )
  }
  if (lookup.status !== "pending" || !lookup.site) {
    const title = {
      accepted: "This link was already used",
      expired: "This link has expired",
      revoked: "This link was withdrawn",
      pending: "This link doesn’t work",
    }[lookup.status]
    return (
      <Notice icon={<TimerOffIcon />} title={title} token={token}>
        <p>
          {transfer ? "Ownership links" : "Invite links"} work once and only for a limited time. Ask the person who sent
          it for a new one; nothing else is needed on your side.
        </p>
      </Notice>
    )
  }

  const returnTo = encodeURIComponent("/invite")
  const leave = () => storage()?.setItem(AUTO_KEY, "1")
  const from = lookup.from || "The owner"
  return (
    <Offer
      lookup={lookup}
      icon={transfer ? <CrownIcon /> : <MailOpenIcon />}
      eyebrow={transfer ? "Ownership offer" : "You’re invited"}
      title={lookup.site.title}
      token={token}
    >
      {user ? (
        <div className="flex flex-col gap-4">
          <p className="text-sm leading-relaxed text-muted-foreground">
            You’re signed in as <span className="font-medium break-all text-foreground">{user.email || "your account"}</span>.{" "}
            {lookup.email && user.email && lookup.email !== user.email
              ? `It was sent to ${lookup.email}; accepting adds it to the account you’re signed in with.`
              : transfer
                ? `Accepting makes this account the owner. ${from} stays on as a manager.`
                : "Accepting adds the site to this account, and you’ll go straight to it."}
          </p>
          <Button variant="brand" size="xl" onClick={accept} disabled={busy} data-testid="accept-invite">
            {busy && <Spinner data-icon="inline-start" />}
            {transfer ? (busy ? "Taking ownership…" : "Accept ownership") : busy ? "Accepting…" : "Accept invitation"}
          </Button>
        </div>
      ) : (
        <div className="flex flex-col gap-5">
          <ol className="flex flex-col gap-3 rounded-2xl bg-card p-4 text-sm ring-1 ring-border" aria-label="What happens next">
            <Step n={1}>
              <span className="font-medium text-foreground">Make a free account</span> with your email and a passkey: your
              fingerprint, face, or device PIN. No password to remember.
            </Step>
            <Step n={2}>
              {transfer ? (
                <>
                  <span className="font-medium text-foreground">Accept</span>, and the site moves to your account.
                </>
              ) : (
                <>
                  <span className="font-medium text-foreground">You land on the site</span>, and it stays in your list for
                  next time.
                </>
              )}
            </Step>
          </ol>
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
          </div>
        </div>
      )}
      <FormError>{error}</FormError>
    </Offer>
  )
}

function Step({ n, children }: { n: number; children: React.ReactNode }) {
  return (
    <li className="flex gap-3">
      <span
        aria-hidden="true"
        className="grid size-6 shrink-0 place-items-center rounded-full bg-brand-soft font-mono text-xs font-medium text-brand-strong"
      >
        {n}
      </span>
      <span className="leading-relaxed text-muted-foreground">{children}</span>
    </li>
  )
}

const roleText: Record<string, string> = {
  viewer: "as a viewer.",
  manager: "as a manager, so you can also publish updates and invite viewers.",
  owner: "as its owner.",
}

function Offer({
  lookup,
  icon,
  eyebrow,
  title,
  token,
  children,
}: {
  lookup: Lookup
  icon: React.ReactNode
  eyebrow: string
  title: string
  token: string
  children: React.ReactNode
}) {
  const transfer = lookup.kind === "transfer"
  const from = lookup.from || "Someone"
  return (
    <div className="flex flex-col gap-7" data-testid="invite-ready" data-kind={lookup.kind}>
      <AuthHeading icon={icon} eyebrow={eyebrow} title={title} titleTestId="invite-site-title">
        <p data-testid="invite-summary">
          {transfer ? (
            <>
              <span className="font-medium text-foreground">{from}</span> is offering you ownership of this site.
            </>
          ) : (
            <>
              <span className="font-medium text-foreground">{from}</span> invited you to this site{" "}
              {roleText[lookup.role ?? "viewer"]}
            </>
          )}
        </p>
      </AuthHeading>
      {children}
      <TokenTail token={token} />
    </div>
  )
}

/** The link's last characters, to tell links apart without showing them. */
function TokenTail({ token }: { token: string }) {
  return (
    <p className="font-mono text-xs text-muted-foreground">
      Link ending{" "}
      <span className="text-foreground" data-testid="invite-token-tail">
        …{token.slice(-6)}
      </span>
    </p>
  )
}

function Notice({
  icon,
  tone = "muted",
  title,
  token,
  action,
  children,
}: {
  icon: React.ReactNode
  tone?: "brand" | "muted"
  title: string
  token: string
  action?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-7" data-testid="invite-unusable">
      <AuthHeading icon={icon} tone={tone} eyebrow="Invitation" title={title}>
        {children}
      </AuthHeading>
      {action ?? (
        <Button asChild variant="outline" size="xl">
          <Link href="/">Go to Handbill</Link>
        </Button>
      )}
      <TokenTail token={token} />
    </div>
  )
}

function MissingView() {
  return (
    <div className="flex flex-col gap-7" data-testid="invite-missing">
      <AuthHeading icon={<LinkIcon />} tone="muted" eyebrow="Invitation" title="This invite link is incomplete">
        <p>
          Open the whole link from the message you received. It ends with{" "}
          <code className="font-mono text-foreground">#token=</code> and a long code.
        </p>
      </AuthHeading>
      <Button asChild variant="outline" size="xl">
        <Link href="/">Go to Handbill</Link>
      </Button>
    </div>
  )
}

function LoadingView() {
  return (
    <div className="flex flex-col gap-7" aria-busy="true" aria-label="Checking your invitation">
      <div className="flex flex-col gap-2.5">
        <Skeleton className="mb-2 size-11 rounded-xl" />
        <Skeleton className="h-4 w-24" />
        <Skeleton className="h-9 w-64 max-w-full" />
        <Skeleton className="h-4 w-full" />
      </div>
      <Skeleton className="h-28 w-full rounded-2xl" />
      <Skeleton className="h-11 w-full rounded-xl" />
    </div>
  )
}
