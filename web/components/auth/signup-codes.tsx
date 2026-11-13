"use client"

import { ArrowRightIcon } from "lucide-react"
import { useSearchParams } from "next/navigation"
import { useEffect, useRef, useState } from "react"

import { AuthHeading } from "@/components/auth/parts"
import { RecoveryCodes } from "@/components/auth/recovery-codes"
import { Button } from "@/components/ui/button"
import { FormError } from "@/components/ui/form-error"
import { api, errorMessage, loginHref, safeReturn, useMe } from "@/lib/api"

/**
 * The screen right after sign-up (passkey or Google): issues the account's
 * first ten recovery codes and shows them once.
 */
export function SignupCodes() {
  const returnTo = safeReturn(useSearchParams().get("return"))
  const { user, loading } = useMe()
  const [codes, setCodes] = useState<string[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const issued = useRef(-1)

  useEffect(() => {
    if (loading) return
    if (!user) {
      window.location.replace(loginHref("/signup/codes"))
      return
    }
    if (issued.current === attempt) return
    issued.current = attempt
    api<{ codes: string[] }>("/app-api/auth/recovery-codes", { method: "POST" })
      .then((r) => setCodes(r.codes))
      .catch((err) => setError(errorMessage(err)))
  }, [loading, user, attempt])

  return (
    <div className="flex flex-col gap-7">
      <AuthHeading
        eyebrow={
          <span className="inline-flex items-center gap-2">
            <span className="inline-block size-1.5 rounded-full bg-success" aria-hidden="true" />
            Account created
          </span>
        }
        title="Keep these somewhere safe"
      >
        <p>
          If you ever lose every passkey, one of these codes gets you back in. Each works once, and this is the only
          time you&rsquo;ll see them.
        </p>
      </AuthHeading>

      {error ? (
        <FormError>
          <p>{error}</p>
          <button
            type="button"
            className="mt-1 font-medium underline underline-offset-4"
            onClick={() => {
              setError(null)
              setAttempt((n) => n + 1)
            }}
          >
            Try again
          </button>
        </FormError>
      ) : (
        <RecoveryCodes codes={codes} email={user?.email} />
      )}

      <div className="flex flex-col gap-4">
        <label className="flex cursor-pointer items-start gap-3 text-sm leading-snug pointer-coarse:min-h-11">
          <input
            type="checkbox"
            className="mt-0.5 size-4 shrink-0 cursor-pointer accent-(--brand-strong)"
            checked={saved}
            onChange={(e) => setSaved(e.target.checked)}
            disabled={!codes}
          />
          <span>I&rsquo;ve saved my recovery codes in a password manager or another safe place.</span>
        </label>
        <Button
          type="button"
          variant="brand"
          size="xl"
          disabled={!saved || !codes}
          onClick={() => window.location.assign(returnTo ?? "/")}
        >
          Continue to Handbill
          <ArrowRightIcon data-icon="inline-end" />
        </Button>
        <p className="text-center font-mono text-[0.7rem] text-muted-foreground">
          You can issue a new set any time in Settings → Security.
        </p>
      </div>
    </div>
  )
}
