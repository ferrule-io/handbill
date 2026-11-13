import type { Metadata } from "next"

import { VerifyEmail } from "@/components/auth/verify-email"
import { AuthShell } from "@/components/layout/auth-shell"

export const metadata: Metadata = { title: "Verify email" }

// /verify-email#token=<token>: the token stays in the fragment, so link
// scanners that fetch the page never consume it and it never reaches logs.
export default function VerifyEmailPage() {
  return (
    <AuthShell
      aside={
        <>
          Signed, sealed, <em>verified.</em>
        </>
      }
    >
      <VerifyEmail />
    </AuthShell>
  )
}
