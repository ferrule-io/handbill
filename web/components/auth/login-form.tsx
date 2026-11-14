"use client"

import { browserSupportsWebAuthn } from "@simplewebauthn/browser"
import { ArrowRightIcon, FingerprintIcon, KeyRoundIcon, MailIcon } from "lucide-react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { useEffect, useState } from "react"

import { googleErrors, signInWithPasskey, startGoogle } from "@/components/auth/ceremonies"
import { GoogleIcon } from "@/components/auth/google-icon"
import { AuthFooterLink, AuthHeading, OrDivider, PasskeyNote } from "@/components/auth/parts"
import { Button } from "@/components/ui/button"
import { FormError } from "@/components/ui/form-error"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Spinner } from "@/components/ui/spinner"
import { errorMessage, safeReturn, useMe } from "@/lib/api"

/**
 * Sign-in: a usernameless passkey first (the browser lists this site's
 * passkeys), an email-first fallback for passkeys that aren't
 * discoverable, and Google.
 */
export function LoginForm() {
  const params = useSearchParams()
  const returnTo = safeReturn(params.get("return"))
  const errorParam = params.get("error")
  const { user, loading, recovery, googleEnabled } = useMe()
  const [mode, setMode] = useState<"passkey" | "email">("passkey")
  const [email, setEmail] = useState("")
  const [busy, setBusy] = useState<"passkey" | "email" | "google" | null>(null)
  const [error, setError] = useState<string | null>(
    errorParam ? (googleErrors[errorParam] ?? "Sign-in didn't complete. Try again.") : null
  )
  const [signedIn, setSignedIn] = useState(false)

  const destination = returnTo ?? "/"
  const query = returnTo ? `?return=${encodeURIComponent(returnTo)}` : ""

  useEffect(() => {
    if (loading || signedIn) return
    if (user) window.location.replace(destination)
    else if (recovery) window.location.replace("/login/recover" + query)
  }, [loading, user, recovery, signedIn, destination, query])

  async function passkey(withEmail?: string) {
    setError(null)
    if (!browserSupportsWebAuthn()) {
      setError("This browser doesn't support passkeys. Continue with Google, or use a recovery code.")
      return
    }
    setBusy(withEmail === undefined ? "passkey" : "email")
    try {
      setSignedIn(true)
      await signInWithPasskey(withEmail)
      // A full load: the return path may be a server route (OAuth consent).
      window.location.assign(destination)
    } catch (err) {
      setSignedIn(false)
      setError(errorMessage(err))
      setBusy(null)
    }
  }

  async function google() {
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
      <AuthHeading eyebrow="Sign in" title="Welcome back">
        Use a passkey saved on this device, your phone, or a security key.
      </AuthHeading>

      <div className="flex flex-col gap-4">
        {mode === "passkey" ? (
          <>
            <Button type="button" variant="brand" size="xl" disabled={busy !== null} onClick={() => passkey()}>
              {busy === "passkey" ? <Spinner data-icon="inline-start" /> : <FingerprintIcon data-icon="inline-start" />}
              {busy === "passkey" ? "Waiting for your passkey…" : "Sign in with passkey"}
            </Button>
            <PasskeyNote>
              No email needed: your browser shows the passkeys it has for this site.{" "}
              <button
                type="button"
                className="text-foreground underline decoration-border underline-offset-2 hover:decoration-brand pointer-coarse:-my-3.5 pointer-coarse:inline-block pointer-coarse:py-3.5"
                onClick={() => {
                  setMode("email")
                  setError(null)
                }}
              >
                Use my email instead
              </button>
            </PasskeyNote>
          </>
        ) : (
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault()
              passkey(email)
            }}
          >
            <div className="flex flex-col gap-2">
              <Label htmlFor="email">Email</Label>
              <Input
                id="email"
                type="email"
                required
                autoFocus
                autoComplete="email webauthn"
                placeholder="you@example.com"
                className="h-11"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                disabled={busy !== null}
              />
            </div>
            <Button type="submit" variant="brand" size="xl" disabled={busy !== null}>
              {busy === "email" ? <Spinner data-icon="inline-start" /> : <MailIcon data-icon="inline-start" />}
              {busy === "email" ? "Waiting for your passkey…" : "Continue with passkey"}
              {busy !== "email" && <ArrowRightIcon data-icon="inline-end" />}
            </Button>
            <PasskeyNote>
              <button
                type="button"
                className="text-foreground underline decoration-border underline-offset-2 hover:decoration-brand pointer-coarse:-my-3.5 pointer-coarse:inline-block pointer-coarse:py-3.5"
                onClick={() => {
                  setMode("passkey")
                  setError(null)
                }}
              >
                Sign in without typing an email
              </button>
            </PasskeyNote>
          </form>
        )}
        <FormError>{error}</FormError>
        {googleEnabled && (
          <>
            <OrDivider />
            <Button type="button" variant="outline" size="xl" onClick={google} disabled={busy !== null}>
              {busy === "google" ? <Spinner className="size-4" /> : <GoogleIcon className="size-4" />}
              Continue with Google
            </Button>
          </>
        )}
      </div>

      <div className="flex flex-col gap-3 border-t border-border pt-6">
        <Link
          href={"/login/recover" + query}
          className="group flex items-center gap-3 rounded-lg text-sm text-muted-foreground transition-colors hover:text-foreground pointer-coarse:min-h-11"
        >
          <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-muted text-foreground ring-1 ring-border [&_svg]:size-4">
            <KeyRoundIcon />
          </span>
          <span>
            Lost your passkey? <span className="font-medium text-foreground group-hover:underline group-hover:decoration-brand group-hover:decoration-2 group-hover:underline-offset-4">Use a recovery code</span>
          </span>
        </Link>
        <AuthFooterLink>
          New here? <Link href={"/signup" + query}>Create an account</Link>
        </AuthFooterLink>
      </div>
    </div>
  )
}
