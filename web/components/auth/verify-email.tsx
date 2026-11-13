"use client"

import { ArrowRightIcon, MailCheckIcon, MailXIcon } from "lucide-react"
import Link from "next/link"
import { useEffect, useRef, useState } from "react"

import { AuthHeading, Spinner } from "@/components/auth/parts"
import { Button } from "@/components/ui/button"
import { api, errorMessage, refreshMe } from "@/lib/api"
import { fragmentParam, useHash } from "@/lib/params"

/** /verify-email#token=…: consumes an email verification link. */
export function VerifyEmail() {
  const hash = useHash()
  const [state, setState] = useState<{ kind: "pending" } | { kind: "done"; email: string } | { kind: "error"; message: string }>({
    kind: "pending",
  })
  const sent = useRef(false)

  const token = hash === null ? null : fragmentParam(hash, "token")

  useEffect(() => {
    if (!token || sent.current) return
    sent.current = true
    // Keep the token out of the address bar and history.
    history.replaceState(null, "", location.pathname)
    api<{ email: string }>("/app-api/auth/email/verify", { method: "POST", body: { token } })
      .then((r) => {
        setState({ kind: "done", email: r.email })
        refreshMe().catch(() => {})
      })
      .catch((err) => setState({ kind: "error", message: errorMessage(err) }))
  }, [token])

  const view =
    state.kind === "pending" && hash !== null && !token
      ? { kind: "error" as const, message: "This link is incomplete. Open the full link from the email." }
      : state

  if (view.kind === "pending") {
    return (
      <div className="flex items-center gap-3 text-sm text-muted-foreground">
        <Spinner className="size-4" /> Confirming your email…
      </div>
    )
  }
  if (view.kind === "error") {
    return (
      <div className="flex flex-col gap-6">
        <Icon tone="error" />
        <AuthHeading eyebrow="Email verification" title="That link didn’t work">
          {view.message} You can send a fresh link from Settings → Security.
        </AuthHeading>
        <Button asChild variant="outline" size="xl">
          <Link href="/settings/security">Open security settings</Link>
        </Button>
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-6">
      <Icon tone="ok" />
      <AuthHeading eyebrow="Email verification" title="You’re verified">
        <span className="font-mono text-foreground">{view.email}</span> is confirmed. Invitations sent to it now reach
        you automatically.
      </AuthHeading>
      <Button asChild variant="brand" size="xl">
        <Link href="/">
          Go to your sites <ArrowRightIcon data-icon="inline-end" />
        </Link>
      </Button>
    </div>
  )
}

function Icon({ tone }: { tone: "ok" | "error" }) {
  return (
    <span
      className={
        tone === "ok"
          ? "grid size-12 place-items-center rounded-2xl bg-brand-soft text-brand ring-1 ring-brand/20 [&_svg]:size-5"
          : "grid size-12 place-items-center rounded-2xl bg-destructive/10 text-destructive ring-1 ring-destructive/20 [&_svg]:size-5"
      }
    >
      {tone === "ok" ? <MailCheckIcon /> : <MailXIcon />}
    </span>
  )
}
