import type { Metadata } from "next"
import { Suspense } from "react"

import { SiteDetail, SiteDetailSkeleton } from "@/components/site/site-detail"

export const metadata: Metadata = { title: "Site" }

// /site?id=<site-id>: the id is read on the client (useSearchParams needs a
// Suspense boundary in a static export; the fallback is what gets prerendered).
export default function SitePage() {
  return (
    <Suspense fallback={<SiteDetailSkeleton />}>
      <SiteDetail />
    </Suspense>
  )
}
