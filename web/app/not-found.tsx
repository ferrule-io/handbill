import { ArrowLeftIcon } from "lucide-react"
import type { Metadata } from "next"
import Link from "next/link"

import { Wordmark } from "@/components/brand/wordmark"
import { Button } from "@/components/ui/button"

export const metadata: Metadata = { title: "Not found" }

// Exported as 404.html; the Go server returns it with status 404 for any
// path that is not a file in the export.
export default function NotFound() {
  return (
    <div className="bg-press flex min-h-svh flex-col">
      <div className="p-5 sm:p-8">
        <Link href="/" className="rounded-md">
          <Wordmark />
        </Link>
      </div>
      <main className="flex flex-1 flex-col items-center justify-center gap-6 px-5 pb-24 text-center">
        <p className="eyebrow text-brand-strong">Error 404</p>
        <h1 className="text-[clamp(3.75rem,2rem+9vw,10rem)] leading-none font-medium [font-variation-settings:'SOFT'_100,'WONK'_1]">
          Misprint<span className="text-brand">.</span>
        </h1>
        <p className="max-w-sm text-muted-foreground" data-testid="not-found">
          There’s no page at this address. It may have moved, or the link was cut short.
        </p>
        <Button asChild variant="outline" size="lg">
          <Link href="/">
            <ArrowLeftIcon data-icon="inline-start" /> Back to your sites
          </Link>
        </Button>
      </main>
    </div>
  )
}
