import type { Metadata } from "next"
import { Suspense } from "react"

import { LoginForm } from "@/components/auth/login-form"
import { AuthShell } from "@/components/layout/auth-shell"

export const metadata: Metadata = { title: "Sign in" }

// /login?return=<same-origin path>&error=<code>
export default function LoginPage() {
  return (
    <AuthShell
      aside={
        <>
          Fresh off the press, <em>right where you left it.</em>
        </>
      }
    >
      <Suspense>
        <LoginForm />
      </Suspense>
    </AuthShell>
  )
}
