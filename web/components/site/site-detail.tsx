"use client"

import { ArrowLeftIcon, ArrowUpRightIcon, FileSearchIcon, LockIcon, PencilIcon, RefreshCwIcon } from "lucide-react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { useState } from "react"

import { Container, Stack } from "@/components/layout/container"
import { EmptyState } from "@/components/layout/empty-state"
import { PageHeader, Section } from "@/components/layout/page-header"
import { can, useApi, type Site, type Version } from "@/components/site/data"
import { SiteMembers } from "@/components/site/members"
import { WelcomeBanner } from "@/components/site/members-welcome"
import { PublicLink } from "@/components/site/public-link"
import { SiteOverview } from "@/components/site/site-overview"
import { SiteVersions } from "@/components/site/site-versions"
import { Transfer } from "@/components/site/transfer"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { isSiteId } from "@/lib/params"

const backToSites = (
  <Button asChild variant="outline">
    <Link href="/">
      <ArrowLeftIcon /> Back to sites
    </Link>
  </Button>
)

export function SiteDetail() {
  const id = useSearchParams().get("id")
  if (!isSiteId(id)) {
    return (
      <Container className="pt-14">
        <EmptyState icon={<FileSearchIcon />} title="No such site" actions={backToSites}>
          <p>
            This link doesn’t point at a site id. Site ids are twelve lowercase letters and digits
            2–7, like <code className="font-mono text-foreground">k3vq7m2xa4pd</code>.
          </p>
        </EmptyState>
      </Container>
    )
  }
  // Keyed so client-side navigation to another site starts from scratch.
  return <SiteView key={id} id={id} />
}

function SiteEyebrow({ id }: { id: string }) {
  return (
    <span className="inline-flex items-center gap-2">
      Site
      <Badge variant="outline" className="font-mono normal-case tracking-normal" data-testid="site-id">
        {id}
      </Badge>
    </span>
  )
}

type SiteResponse = { site: Site; currentVersion: Version | null }

function SiteView({ id }: { id: string }) {
  const [load, reload] = useApi<SiteResponse>(`/app-api/sites/${id}`)
  const [editing, setEditing] = useState(false)
  const welcome = useSearchParams().get("welcome") === "1"

  if (load.state === "loading") return <SiteDetailSkeleton id={id} />
  if (load.state === "error") {
    const missing = load.error.status === 404
    return (
      <Container>
        <Stack gap="loose">
          <PageHeader eyebrow={<SiteEyebrow id={id} />} title={missing ? "Site unavailable" : "Something went wrong"} />
          {missing ? (
            <EmptyState icon={<LockIcon />} title="You don’t have access to this site" actions={backToSites}>
              <p>
                Either it doesn’t exist, or it hasn’t been shared with the account you’re signed in
                with. Ask the person who sent you the link to invite you.
              </p>
            </EmptyState>
          ) : (
            <EmptyState
              title="Couldn’t load this site"
              actions={
                <Button variant="outline" onClick={reload}>
                  <RefreshCwIcon /> Try again
                </Button>
              }
            >
              <p>{load.error.message}</p>
            </EmptyState>
          )}
        </Stack>
      </Container>
    )
  }

  const { site, currentVersion } = load.data
  return (
    <Container>
      <Stack gap="loose">
        <PageHeader
          eyebrow={<SiteEyebrow id={id} />}
          title={<span data-testid="site-title">{site.title}</span>}
          description={site.description || undefined}
          actions={
            <>
              {can.edit(site.role) && !editing && (
                <Button variant="outline" size="lg" onClick={() => setEditing(true)} data-testid="edit-details">
                  <PencilIcon /> Edit details
                </Button>
              )}
              {currentVersion && (
                <Button asChild variant="brand" size="lg">
                  <a href={site.url} target="_blank" rel="noreferrer" data-testid="open-site-link">
                    Open site <ArrowUpRightIcon />
                  </a>
                </Button>
              )}
            </>
          }
        />
        {welcome && <WelcomeBanner site={site} published={currentVersion !== null} />}
        <Section title="Overview" description="Where the site lives and what it serves right now.">
          <SiteOverview
            site={site}
            currentVersion={currentVersion}
            editing={editing}
            onEditDone={(saved) => {
              setEditing(false)
              if (saved) reload()
            }}
          />
        </Section>
        {can.publish(site.role) && (
          <Section
            title="Versions"
            description="Every publish is kept. Rolling back switches what viewers see at once; newer versions stay here."
          >
            <SiteVersions site={site} onRollback={reload} />
          </Section>
        )}
        {can.manageViewers(site.role) && (
          <Section
            title="Members"
            description={
              can.manageManagers(site.role)
                ? "Invite people by email as viewers or managers. Changes apply on their next page load."
                : "Invite viewers by email and remove them. Only the owner adds or removes managers."
            }
          >
            <SiteMembers site={site} />
          </Section>
        )}
        {can.manageViewers(site.role) && (
          <Section
            title="Public link"
            description="An optional link that lets anyone who has it view the site without an account."
          >
            <PublicLink site={site} />
          </Section>
        )}
        {can.transfer(site.role) && (
          <Section title="Ownership" description="Hand this site to another account. It completes when they accept.">
            <Transfer site={site} />
          </Section>
        )}
      </Stack>
    </Container>
  )
}

export function SiteDetailSkeleton({ id }: { id?: string }) {
  return (
    <Container>
      <div className="flex flex-col gap-12" aria-busy="true">
        <div className="flex flex-col gap-3 pt-10 sm:pt-14">
          {id ? <p className="eyebrow"><SiteEyebrow id={id} /></p> : <Skeleton className="h-3 w-24" />}
          <Skeleton className="h-10 w-72 max-w-full" />
          <Skeleton className="h-4 w-96 max-w-full" />
        </div>
        <div className="grid gap-5 md:grid-cols-[14rem_1fr] md:gap-10">
          <Skeleton className="h-5 w-28" />
          <Skeleton className="h-56 w-full rounded-2xl" />
        </div>
      </div>
    </Container>
  )
}
