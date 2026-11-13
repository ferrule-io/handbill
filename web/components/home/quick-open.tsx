"use client"

import { ArrowRightIcon } from "lucide-react"
import Link from "next/link"
import { useId, useState } from "react"

import { Section } from "@/components/layout/page-header"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { fragmentParam, isSiteId } from "@/lib/params"

/** Accepts a pasted invite link (or bare token) and returns the token. */
function inviteToken(value: string): string {
  const v = value.trim()
  const hash = v.indexOf("#")
  return hash >= 0 ? (fragmentParam(v.slice(hash), "token") ?? "") : v
}

/**
 * Open a site by id or an invite by its link. Both targets are fixed routes
 * with the parameter in the query or fragment, navigated client-side.
 */
export function QuickOpen() {
  const siteField = useId()
  const inviteField = useId()
  const [site, setSite] = useState("")
  const [invite, setInvite] = useState("")
  const siteId = site.trim().toLowerCase()
  const token = inviteToken(invite)

  return (
    <Section
      title="Jump to"
      description="Open a site by its id, or paste an invite link someone sent you."
    >
      <div className="grid gap-6 rounded-2xl bg-card p-5 shadow-press ring-1 ring-border sm:grid-cols-2 sm:p-6">
        <div className="flex flex-col gap-2">
          <Label htmlFor={siteField}>Site id</Label>
          <div className="flex gap-2">
            <Input
              id={siteField}
              value={site}
              onChange={(e) => setSite(e.target.value)}
              placeholder="k3vq7m2xa4pd"
              spellCheck={false}
              autoComplete="off"
              className="font-mono"
            />
            <Button asChild variant="outline" size="icon-lg" aria-label="Open site" aria-disabled={!isSiteId(siteId)}>
              <Link
                href={`/site?id=${encodeURIComponent(siteId)}`}
                data-testid="open-site"
                className="aria-disabled:pointer-events-none aria-disabled:opacity-50"
              >
                <ArrowRightIcon />
              </Link>
            </Button>
          </div>
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor={inviteField}>Invite link</Label>
          <div className="flex gap-2">
            <Input
              id={inviteField}
              value={invite}
              onChange={(e) => setInvite(e.target.value)}
              placeholder="https://…/invite#token=…"
              spellCheck={false}
              autoComplete="off"
              className="font-mono"
            />
            <Button asChild variant="outline" size="icon-lg" aria-label="Open invite" aria-disabled={!token}>
              <Link
                href={`/invite#token=${encodeURIComponent(token)}`}
                data-testid="open-invite"
                className="aria-disabled:pointer-events-none aria-disabled:opacity-50"
              >
                <ArrowRightIcon />
              </Link>
            </Button>
          </div>
        </div>
      </div>
    </Section>
  )
}
