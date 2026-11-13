"use client"

import { browserSupportsWebAuthn } from "@simplewebauthn/browser"
import { ArrowLeftIcon, FingerprintIcon, ShieldCheckIcon } from "lucide-react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { useEffect, useState } from "react"

import { addPasskey } from "@/components/auth/ceremonies"
import { AuthFooterLink, AuthHeading, FormError, PasskeyNote, Spinner } from "@/components/auth/parts"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { api, errorMessage, refreshMe, safeReturn, useMe } from "@/lib/api"

/**
 * Recovery: an email and one recovery code open a short, restricted
 * session whose only power is registering a new passkey; doing so signs
 * the account in normally.
 */
export function RecoverForm() {
  const returnTo = safeReturn(useSearchParams().get("return"))
  const { user, loading, recovery } = useMe()
  const [email, setEmail] = useState("")
  const [code, setCode] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [finished, setFinished] = useState(false)

  useEffect(() => {
    if (!loading && user && !finished) window.location.replace(returnTo ?? "/")
  }, [loading, user, finished, returnTo])

  async function submitCode(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      await api("/app-api/auth/recovery/signin", { method: "POST", body: { email, code } })
      await refreshMe()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  async function register() {
    setError(null)
    if (!browserSupportsWebAuthn()) {
      setError("This browser can't create passkeys. Try another browser or device.")
      return
    }
    setBusy(true)
    try {
      setFinished(true)
      await addPasskey()
      window.location.assign(returnTo ?? "/settings/security?recovered=1")
    } catch (err) {
      setFinished(false)
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  if (recovery) {
    return (
      <div className="flex flex-col gap-8">
        <AuthHeading
          eyebrow={
            <span className="inline-flex items-center gap-2 text-success">
              <ShieldCheckIcon className="size-3.5" aria-hidden="true" /> Code accepted
            </span>
          }
          title="Now, a new passkey"
        >
          Create a passkey on this device to finish signing in. Until you do, this session can&rsquo;t do anything
          else, and it expires in 15 minutes.
        </AuthHeading>
        <div className="flex flex-col gap-4">
          <Button type="button" variant="brand" size="xl" onClick={register} disabled={busy}>
            {busy ? <Spinner data-icon="inline-start" /> : <FingerprintIcon data-icon="inline-start" />}
            {busy ? "Waiting for your passkey…" : "Create a new passkey"}
          </Button>
          <PasskeyNote>Afterwards, review your passkeys and issue fresh recovery codes in Settings.</PasskeyNote>
          <FormError>{error}</FormError>
        </div>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-8">
      <AuthHeading eyebrow="Account recovery" title="Use a recovery code">
        Enter your account&rsquo;s email and one of the ten codes you saved when you signed up.
      </AuthHeading>
      <form className="flex flex-col gap-4" onSubmit={submitCode}>
        <div className="flex flex-col gap-2">
          <Label htmlFor="email">Email</Label>
          <Input
            id="email"
            type="email"
            required
            autoFocus
            autoComplete="email"
            placeholder="you@example.com"
            className="h-11"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            disabled={busy}
          />
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor="code">Recovery code</Label>
          <Input
            id="code"
            required
            autoComplete="one-time-code"
            autoCapitalize="characters"
            spellCheck={false}
            placeholder="XXXX-XXXX-XXXX-XXXX"
            className="h-11 font-mono tracking-[0.12em] uppercase placeholder:tracking-[0.12em]"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            disabled={busy}
          />
        </div>
        <Button type="submit" variant="brand" size="xl" disabled={busy}>
          {busy && <Spinner data-icon="inline-start" />}
          Continue
        </Button>
        <FormError>{error}</FormError>
      </form>
      <AuthFooterLink>
        <Link href={"/login" + (returnTo ? `?return=${encodeURIComponent(returnTo)}` : "")} className="inline-flex items-center gap-1.5">
          <ArrowLeftIcon className="size-3.5" aria-hidden="true" /> Back to sign in
        </Link>
      </AuthFooterLink>
    </div>
  )
}
