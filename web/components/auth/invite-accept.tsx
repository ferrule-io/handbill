"use client"

import { LinkIcon, MailOpenIcon } from "lucide-react"
import Link from "next/link"

import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { fragmentParam, useHash } from "@/lib/params"

export function InviteAccept() {
  const hash = useHash()

  if (hash === null) {
    return (
      <div className="flex flex-col gap-4" aria-busy="true">
        <Skeleton className="h-3 w-20" />
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-4 w-full" />
      </div>
    )
  }

  const token = fragmentParam(hash, "token")
  if (!token) {
    return (
      <div className="flex flex-col gap-4" data-testid="invite-missing">
        <div className="grid size-11 place-items-center rounded-xl bg-muted [&_svg]:size-5">
          <LinkIcon />
        </div>
        <h1 className="text-[2rem] leading-tight font-medium">This invite link is incomplete</h1>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Open the full link from the message you received. It ends with{" "}
          <code className="font-mono text-foreground">#token=…</code>
        </p>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-8" data-testid="invite-ready">
      <div className="flex flex-col gap-3">
        <div className="grid size-11 place-items-center rounded-xl bg-brand-soft text-brand [&_svg]:size-5">
          <MailOpenIcon />
        </div>
        <p className="eyebrow">Invitation</p>
        <h1 className="text-[2rem] leading-tight font-medium">You’ve been invited to a site</h1>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Create an account or sign in to accept it. It only takes a moment.
        </p>
      </div>
      <div className="flex items-center justify-between rounded-xl bg-muted px-4 py-3 font-mono text-xs text-muted-foreground">
        <span>invite token</span>
        <span className="text-foreground" data-testid="invite-token-tail">
          …{token.slice(-6)}
        </span>
      </div>
      <div className="flex flex-col gap-3">
        <Button asChild variant="brand" size="xl">
          <Link href="/signup">Create account</Link>
        </Button>
        <Button asChild variant="outline" size="xl">
          <Link href="/login">I already have one</Link>
        </Button>
      </div>
    </div>
  )
}
