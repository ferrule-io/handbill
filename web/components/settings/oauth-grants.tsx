"use client"

import { PlugIcon } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import { toast } from "sonner"

import { clientName, deleteGrant, listGrants, type Grant } from "@/components/consent/data"
import { Section } from "@/components/layout/page-header"
import { formatDate, relative, Row, RowSkeleton } from "@/components/settings/row"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { errorMessage } from "@/lib/api"

/** Connected agents on /settings/security: OAuth grants, disconnectable. */
export function OAuthGrants() {
  const [grants, setGrants] = useState<Grant[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const reload = useCallback(async () => {
    try {
      setGrants((await listGrants()).grants)
      setError(null)
    } catch (err) {
      setError(errorMessage(err))
    }
  }, [])

  useEffect(() => {
    listGrants().then(
      (r) => setGrants(r.grants),
      (err) => setError(errorMessage(err))
    )
  }, [])

  return (
    <Section title="Connected agents" description="MCP clients you’ve authorized to publish on your behalf.">
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : !grants ? (
        <RowSkeleton />
      ) : grants.length === 0 ? (
        <Row icon={<PlugIcon />} title="No connected agents" detail="Authorize an MCP client and it will be listed here." />
      ) : (
        <ul className="flex flex-col gap-3" data-testid="oauth-grants">
          {grants.map((g) => (
            <li key={g.id}>
              <Row
                icon={<PlugIcon />}
                title={clientName(g.client)}
                badge={!g.client.verified && <Badge variant="outline">Unverified</Badge>}
                detail={
                  <>
                    <span className="font-mono text-xs break-all text-foreground">{g.client.host}</span>
                    <br />
                    Connected {formatDate(g.createdAt)}
                    {g.lastUsedAt ? ` · last used ${relative(g.lastUsedAt)}` : " · not used yet"}
                  </>
                }
                action={<Disconnect grant={g} onDone={reload} />}
              />
            </li>
          ))}
        </ul>
      )}
    </Section>
  )
}

function Disconnect({ grant, onDone }: { grant: Grant; onDone: () => Promise<void> }) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const name = clientName(grant.client)
  async function disconnect() {
    setBusy(true)
    try {
      await deleteGrant(grant.id)
      toast.success(`Disconnected ${name}.`)
      setOpen(false)
      await onDone()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger asChild>
        <Button variant="outline" size="sm">
          Disconnect
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Disconnect {name}?</AlertDialogTitle>
          <AlertDialogDescription>
            {name} will lose access immediately: its next request fails, and it has to ask you again to
            reconnect. Sites it published stay as they are.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={disconnect} disabled={busy}>
            {busy ? "Disconnecting…" : "Disconnect"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
