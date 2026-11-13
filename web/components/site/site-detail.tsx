"use client"

import { ArrowLeftIcon, FileSearchIcon, GlobeIcon, HistoryIcon, UsersIcon } from "lucide-react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"

import { Container, Stack } from "@/components/layout/container"
import { EmptyState } from "@/components/layout/empty-state"
import { PageHeader, Section } from "@/components/layout/page-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { isSiteId } from "@/lib/params"

export function SiteDetail() {
  const id = useSearchParams().get("id")

  if (!isSiteId(id)) {
    return (
      <Container className="pt-14">
        <EmptyState
          icon={<FileSearchIcon />}
          title="No such site"
          actions={
            <Button asChild variant="outline">
              <Link href="/">
                <ArrowLeftIcon /> Back to sites
              </Link>
            </Button>
          }
        >
          This link doesn’t point at a site id. Site ids are twelve lowercase letters and digits 2–7, like{" "}
          <code className="font-mono text-foreground">k3vq7m2xa4pd</code>.
        </EmptyState>
      </Container>
    )
  }

  return (
    <Container>
      <Stack gap="loose">
        <PageHeader
          eyebrow={
            <span className="inline-flex items-center gap-2">
              Site
              <Badge variant="outline" className="font-mono normal-case tracking-normal" data-testid="site-id">
                {id}
              </Badge>
            </span>
          }
          title="Site overview"
          description="Settings, members, invites, the public link, and every published version."
        />
        <Section title="Overview" description="Where the site lives and what it serves right now.">
          <Rows icon={<GlobeIcon />} />
        </Section>
        <Section title="Members" description="Owner, managers, viewers, and pending invites.">
          <Rows icon={<UsersIcon />} />
        </Section>
        <Section title="Versions" description="Every publish is kept. Roll back to any of them.">
          <Rows icon={<HistoryIcon />} />
        </Section>
      </Stack>
    </Container>
  )
}

function Rows({ icon }: { icon: React.ReactNode }) {
  return (
    <div className="divide-y divide-border rounded-2xl bg-card ring-1 ring-border">
      {[0, 1, 2].map((i) => (
        <div key={i} className="flex items-center gap-4 px-5 py-4 text-muted-foreground [&_svg]:size-4">
          {icon}
          <Skeleton className="h-3.5 w-40" />
          <Skeleton className="ml-auto h-3.5 w-16" />
        </div>
      ))}
    </div>
  )
}

export function SiteDetailSkeleton() {
  return (
    <Container>
      <div className="flex flex-col gap-6 pt-14" aria-busy="true">
        <Skeleton className="h-3 w-24" />
        <Skeleton className="h-10 w-72" />
        <Skeleton className="h-4 w-96 max-w-full" />
      </div>
    </Container>
  )
}
