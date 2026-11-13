import type { Metadata } from "next"
import { Suspense } from "react"

import { SecuritySettings } from "@/components/settings/security-settings"

export const metadata: Metadata = { title: "Security" }

export default function SecurityPage() {
  return (
    <Suspense>
      <SecuritySettings />
    </Suspense>
  )
}
