import type { Metadata } from "next"
import { Suspense } from "react"

import { SignupForm } from "@/components/auth/signup-form"
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
      <Suspense>
        <SignupForm />
      </Suspense>
    </AuthShell>
  )
}
