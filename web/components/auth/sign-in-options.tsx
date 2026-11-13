import { FingerprintIcon } from "lucide-react"
import Link from "next/link"

import { GoogleIcon } from "@/components/auth/google-icon"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

/**
 * Passkey or Google, the only two ways in. The ceremonies are wired up in
 * the accounts workstream; until then the actions are disabled.
 */
export function SignInOptions({ mode }: { mode: "signup" | "login" }) {
  const signup = mode === "signup"
  return (
    <div className="flex flex-col gap-8">
      <div className="flex flex-col gap-2">
        <p className="eyebrow">{signup ? "Create account" : "Sign in"}</p>
        <h1 className="text-[2rem] leading-tight font-medium">
          {signup ? "Make yourself an account" : "Welcome back"}
        </h1>
        <p className="text-sm leading-relaxed text-muted-foreground">
          {signup
            ? "One step: your email and a passkey, or your Google account."
            : "Use the passkey on this device, or your linked Google account."}
        </p>
      </div>

      <form className="flex flex-col gap-4">
        {signup && (
          <div className="flex flex-col gap-2">
            <Label htmlFor="email">Email</Label>
            <Input id="email" type="email" autoComplete="email" placeholder="you@example.com" className="h-11" />
          </div>
        )}
        <Button type="button" variant="brand" size="xl" disabled>
          <FingerprintIcon data-icon="inline-start" />
          {signup ? "Create with passkey" : "Sign in with passkey"}
        </Button>
        <div className="flex items-center gap-3 font-mono text-[0.7rem] tracking-widest text-muted-foreground uppercase">
          <span className="h-px flex-1 bg-border" />
          or
          <span className="h-px flex-1 bg-border" />
        </div>
        <Button type="button" variant="outline" size="xl" disabled>
          <GoogleIcon className="size-4" />
          Continue with Google
        </Button>
      </form>

      <p className="text-sm text-muted-foreground">
        {signup ? "Already have an account? " : "New here? "}
        <Link
          href={signup ? "/login" : "/signup"}
          className="font-medium text-foreground underline decoration-brand decoration-2 underline-offset-4"
        >
          {signup ? "Sign in" : "Create an account"}
        </Link>
      </p>
    </div>
  )
}
