import type { Metadata } from "next"

import { SignInOptions } from "@/components/auth/sign-in-options"
import { AuthShell } from "@/components/layout/auth-shell"

export const metadata: Metadata = { title: "Create account" }

export default function SignupPage() {
  return (
    <AuthShell
      aside={
        <>
          Your agent made something. <em>Go look at it.</em>
        </>
      }
    >
      <SignInOptions mode="signup" />
    </AuthShell>
  )
}
