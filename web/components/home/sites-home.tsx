"use client"

import { ArrowUpRightIcon, LayersIcon } from "lucide-react"
import Link from "next/link"

import { McpEndpoint } from "@/components/home/mcp-endpoint"
import { EmptyState, LoadError } from "@/components/layout/empty-state"
import { PageHeader } from "@/components/layout/page-header"
import { relativeTime, roleLabel, useApi, type Load, type Site } from "@/components/site/data"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"

/**
 * Header and list of the signed-in user's sites: owned, managed, and shared
 * with them. The MCP endpoint sits in the header once there are sites, and
 * in the empty state before.
 */
export function SitesHome() {
  const [load, reload] = useApi<{ sites: Site[] }>("/app-api/sites")
  const hasSites = load.state === "ready" && load.data.sites.length > 0
  return (
    <>
      <PageHeader
        eyebrow="Workspace"
        title="Your sites"
        description="Everything your agents have published, and everything shared with you."
        actions={hasSites && <McpEndpoint className="hidden md:inline-flex" />}
      />
      <SiteList load={load} reload={reload} />
    </>
  )
}

function SiteList({ load, reload }: { load: Load<{ sites: Site[] }>; reload: () => void }) {
  if (load.state === "loading") {
    return (
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3" aria-busy="true" aria-label="Loading sites">
        {[0, 1, 2].map((i) => (
          <div key={i} className="flex min-h-44 flex-col gap-3 rounded-2xl bg-card p-5 ring-1 ring-border">
            <div className="flex items-start justify-between gap-3">
              <Skeleton className="h-6 w-2/3" />
              <Skeleton className="h-5 w-14 rounded-full" />
            </div>
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-4/5" />
            <div className="mt-auto flex flex-col gap-1.5 pt-2">
              <Skeleton className="h-3 w-1/2" />
              <Skeleton className="h-3 w-1/3" />
            </div>
          </div>
        ))}
      </div>
    )
  }
  if (load.state === "error") {
    return <LoadError title="Couldn’t load your sites" message={load.error.message} onRetry={reload} />
  }
  const sites = load.data.sites
  if (sites.length === 0) {
    return (
      <EmptyState icon={<LayersIcon />} title="Nothing on the press yet" className="py-16">
        <p>
          Agents create and publish sites over MCP. Add this server’s address to an MCP client such as Claude Code, sign
          in when it asks, then ask your agent to publish something. It shows up here, ready to share.
        </p>
        <McpEndpoint className="mt-5" />
        <p className="mt-4">
          Someone sent you a link instead? Open it, or paste it under <span className="text-foreground">Jump to</span>{" "}
          below.
        </p>
      </EmptyState>
    )
  }
  return (
    <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3" data-testid="site-list">
      {sites.map((site) => (
        <li key={site.id}>
          <SiteCard site={site} />
        </li>
      ))}
    </ul>
  )
}

function SiteCard({ site }: { site: Site }) {
  const host = new URL(site.url).hostname
  return (
    <article className="group/site relative flex h-full min-h-44 flex-col gap-3 rounded-2xl bg-card p-5 shadow-press ring-1 ring-border transition-[box-shadow,transform] hover:-translate-y-0.5 hover:ring-foreground/20 has-focus-visible:ring-2 has-focus-visible:ring-ring">
      <div className="flex items-start justify-between gap-3">
        <h3 className="min-w-0 text-lg leading-snug font-medium text-pretty break-words">
          <Link
            href={`/site?id=${site.id}`}
            className="outline-none after:absolute after:inset-0 after:rounded-2xl focus-visible:underline"
            data-testid="site-card-link"
          >
            {site.title}
          </Link>
        </h3>
        <Badge variant={site.role === "viewer" ? "secondary" : "outline"} className="shrink-0">
          {roleLabel[site.role]}
        </Badge>
      </div>
      {site.description ? (
        <p className="line-clamp-2 text-sm leading-relaxed text-muted-foreground">{site.description}</p>
      ) : (
        <p className="text-sm text-muted-foreground italic">No description</p>
      )}
      <div className="mt-auto flex items-center justify-between gap-3 pt-2">
        <div className="flex min-w-0 flex-col gap-0.5">
          <span className="truncate font-mono text-xs text-muted-foreground">{host}</span>
          <span className="text-xs text-muted-foreground">
            {site.currentVersionId ? (
              <>Updated {relativeTime(site.updatedAt)}</>
            ) : (
              <span className="text-brand-strong">Not published yet</span>
            )}
          </span>
        </div>
        {site.currentVersionId && (
          <Button
            asChild
            variant="ghost"
            size="icon-sm"
            className="relative z-10 shrink-0 text-muted-foreground hover:text-foreground"
          >
            <a href={site.url} target="_blank" rel="noreferrer" aria-label={`Open ${site.title} in a new tab`}>
              <ArrowUpRightIcon />
            </a>
          </Button>
        )}
      </div>
    </article>
  )
}
