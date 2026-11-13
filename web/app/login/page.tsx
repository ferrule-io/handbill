import type { Metadata } from "next"

import { SignInOptions } from "@/components/auth/sign-in-options"
import { AuthShell } from "@/components/layout/auth-shell"

export const metadata: Metadata = { title: "Sign in" }

export default function LoginPage() {
  return (
    <AuthShell
      aside={
        <>
          Fresh off the press, <em>right where you left it.</em>
        </>
      }
    >
      <SignInOptions mode="login" />
    </AuthShell>
  )
}
