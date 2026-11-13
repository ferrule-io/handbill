import type { Metadata } from "next"
import { Suspense } from "react"

import { RecoverForm } from "@/components/auth/recover-form"
import { AuthShell } from "@/components/layout/auth-shell"

export const metadata: Metadata = { title: "Account recovery" }

// /login/recover?return=<same-origin path>
export default function RecoverPage() {
  return (
    <AuthShell
      aside={
        <>
          Lost the key? <em>Use the spare.</em>
        </>
      }
    >
      <Suspense>
        <RecoverForm />
      </Suspense>
    </AuthShell>
  )
}
