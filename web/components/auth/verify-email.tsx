"use client"

import { ArrowRightIcon, MailCheckIcon, MailIcon, MailXIcon } from "lucide-react"
import Link from "next/link"
import { useEffect, useRef, useState } from "react"

import { AuthHeading } from "@/components/auth/parts"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
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
      <div className="flex flex-col gap-6" role="status" aria-busy="true">
        <AuthHeading icon={<MailIcon />} tone="muted" eyebrow="Email verification" title="Confirming your email…">
          <p className="flex items-center gap-2">
            <Spinner className="size-4" /> This takes a moment.
          </p>
        </AuthHeading>
      </div>
    )
  }
  if (view.kind === "error") {
    return (
      <div className="flex flex-col gap-6">
        <AuthHeading icon={<MailXIcon />} tone="error" eyebrow="Email verification" title="That link didn’t work">
          <p>{view.message}</p>
          <p>You can send yourself a fresh link from Settings → Security.</p>
        </AuthHeading>
        <Button asChild variant="outline" size="xl">
          <Link href="/settings/security">Open security settings</Link>
        </Button>
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-6">
      <AuthHeading icon={<MailCheckIcon />} eyebrow="Email verification" title="You’re verified">
        <p>
          <span className="font-mono break-all text-foreground">{view.email}</span> is confirmed. Invitations sent to it
          now reach you automatically.
        </p>
      </AuthHeading>
      <Button asChild variant="brand" size="xl">
        <Link href="/">
          Go to your sites <ArrowRightIcon data-icon="inline-end" />
        </Link>
      </Button>
    </div>
  )
}
