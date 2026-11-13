"use client"

import { ArrowUpRightIcon, PartyPopperIcon } from "lucide-react"

import { roleLabel, type Site } from "@/components/site/data"
import { Button } from "@/components/ui/button"

/** Shown on the site page right after accepting an invite or a transfer. */
export function WelcomeBanner({ site, published }: { site: Site; published: boolean }) {
  const owner = site.role === "owner"
  return (
    <div
      className="flex flex-col gap-4 rounded-2xl bg-brand-soft/60 p-5 ring-1 ring-brand/30 sm:flex-row sm:items-center sm:p-6"
      data-testid="welcome-banner"
    >
      <div className="grid size-11 shrink-0 place-items-center rounded-xl bg-card text-brand shadow-press ring-1 ring-border [&_svg]:size-5">
        <PartyPopperIcon />
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <p className="font-medium">{owner ? `You own ${site.title} now` : `You’re in as a ${roleLabel[site.role].toLowerCase()}`}</p>
        <p className="text-sm text-muted-foreground">
          {published
            ? "It opens on its own address and stays in your site list."
            : "Nothing has been published yet. It shows up in your site list, so check back later."}
        </p>
      </div>
      {published && (
        <Button asChild variant="brand" size="lg" className="shrink-0">
          <a href={site.url} target="_blank" rel="noreferrer" data-testid="welcome-open-site">
            Open site <ArrowUpRightIcon />
          </a>
        </Button>
      )}
    </div>
  )
}
