"use client"

import { browserSupportsWebAuthn } from "@simplewebauthn/browser"
import { FingerprintIcon } from "lucide-react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useEffect, useState } from "react"

import { signUpWithPasskey, startGoogle } from "@/components/auth/ceremonies"
import { GoogleIcon } from "@/components/auth/google-icon"
import { AuthFooterLink, AuthHeading, FormError, OrDivider, PasskeyNote, Spinner } from "@/components/auth/parts"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { errorMessage, safeReturn, useMe } from "@/lib/api"

/** One screen, zero barriers: an email and a passkey, or Google. */
export function SignupForm() {
  const router = useRouter()
  const returnTo = safeReturn(useSearchParams().get("return"))
  const { user, loading, googleEnabled } = useMe()
  const [email, setEmail] = useState("")
  const [busy, setBusy] = useState<"passkey" | "google" | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState(false)

  // Already signed in (and not mid-sign-up): nothing to do here.
  useEffect(() => {
    if (!loading && user && !done) window.location.replace(returnTo ?? "/")
  }, [loading, user, done, returnTo])

  const codesHref = "/signup/codes" + (returnTo ? `?return=${encodeURIComponent(returnTo)}` : "")
  const query = returnTo ? `?return=${encodeURIComponent(returnTo)}` : ""

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    if (!browserSupportsWebAuthn()) {
      setError("This browser can't create passkeys. Continue with Google, or try another browser.")
      return
    }
    setBusy("passkey")
    try {
      setDone(true)
      await signUpWithPasskey(email)
      router.push(codesHref)
    } catch (err) {
      setDone(false)
      setError(errorMessage(err))
      setBusy(null)
    }
  }

  async function onGoogle() {
    setError(null)
    setBusy("google")
    try {
      await startGoogle("signin", returnTo)
    } catch (err) {
      setError(errorMessage(err))
      setBusy(null)
    }
  }

  return (
    <div className="flex flex-col gap-8">
      <AuthHeading eyebrow="Create account" title="Make yourself an account">
        One step: your email and a passkey{googleEnabled ? ", or your Google account" : ""}. No password, ever.
      </AuthHeading>

      <form className="flex flex-col gap-4" onSubmit={onSubmit}>
        <div className="flex flex-col gap-2">
          <Label htmlFor="email">Email</Label>
          <Input
            id="email"
            type="email"
            required
            autoComplete="email"
            autoFocus
            placeholder="you@example.com"
            className="h-11"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            disabled={busy !== null}
          />
        </div>
        <Button type="submit" variant="brand" size="xl" disabled={busy !== null}>
          {busy === "passkey" ? <Spinner data-icon="inline-start" /> : <FingerprintIcon data-icon="inline-start" />}
          {busy === "passkey" ? "Waiting for your passkey…" : "Create with passkey"}
        </Button>
        <PasskeyNote>Your device unlocks it with a fingerprint, face, or PIN. The key never leaves it.</PasskeyNote>
        <FormError>{error}</FormError>
        {googleEnabled && (
          <>
            <OrDivider />
            <Button type="button" variant="outline" size="xl" onClick={onGoogle} disabled={busy !== null}>
              {busy === "google" ? <Spinner className="size-4" /> : <GoogleIcon className="size-4" />}
              Continue with Google
            </Button>
          </>
        )}
      </form>

      <AuthFooterLink>
        Already have an account? <Link href={"/login" + query}>Sign in</Link>
      </AuthFooterLink>
    </div>
  )
}
