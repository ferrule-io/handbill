import type { Metadata } from "next"
import { Suspense } from "react"

import { Consent } from "@/components/consent/consent-card"
import { AuthShell } from "@/components/layout/auth-shell"

export const metadata: Metadata = { title: "Connect an app", referrer: "no-referrer" }

// /consent?request=<handle> | /consent?error=<code>, from /oauth/authorize.
export default function ConsentPage() {
  return (
    <AuthShell
      aside={
        <>
          Let your agent publish, <em>and only as you.</em>
        </>
      }
    >
      <Suspense>
        <Consent />
      </Suspense>
    </AuthShell>
  )
}
