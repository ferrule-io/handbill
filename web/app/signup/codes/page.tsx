import type { Metadata } from "next"
import { Suspense } from "react"

import { SignupCodes } from "@/components/auth/signup-codes"
import { AuthShell } from "@/components/layout/auth-shell"

export const metadata: Metadata = { title: "Recovery codes" }

// /signup/codes: the account's first recovery codes, right after sign-up.
export default function SignupCodesPage() {
  return (
    <AuthShell
      aside={
        <>
          One last thing: <em>a way back in.</em>
        </>
      }
    >
      <Suspense>
        <SignupCodes />
      </Suspense>
    </AuthShell>
  )
}
