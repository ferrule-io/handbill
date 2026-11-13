"use client"

import { PlugIcon } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import { toast } from "sonner"

import { clientName, deleteGrant, listGrants, type Grant } from "@/components/consent/data"
import { LoadError } from "@/components/layout/empty-state"
import { Section } from "@/components/layout/page-header"
import { formatDate, relative, Row, RowSkeleton } from "@/components/settings/row"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { errorMessage } from "@/lib/api"

/**
 * Connected agents on /settings/security: OAuth grants, disconnectable.
 * Client names and hosts are client-supplied, so they render inside <bdi>.
 */
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
    <Section title="Connected agents" description="MCP clients you’ve allowed to publish on your behalf.">
      {error && !grants ? (
        <LoadError
          title="Couldn’t load connected agents"
          message={error}
          onRetry={() => {
            setError(null)
            void reload()
          }}
        />
      ) : !grants ? (
        <RowSkeleton />
      ) : grants.length === 0 ? (
        <Row
          icon={<PlugIcon />}
          title="No connected agents"
          detail="When you allow an MCP client to publish as you, it appears here and you can disconnect it."
        />
      ) : (
        <ul className="flex flex-col gap-3" data-testid="oauth-grants">
          {grants.map((g) => (
            <li key={g.id}>
              <Row
                icon={<PlugIcon />}
                title={<bdi>{clientName(g.client)}</bdi>}
                badge={!g.client.verified && <Badge variant="outline">Unverified</Badge>}
                detail={
                  <>
                    <span className="font-mono text-xs break-all text-foreground">
                      <bdi>{g.client.host}</bdi>
                    </span>
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
  const name = <bdi>{clientName(grant.client)}</bdi>
  return (
    <ConfirmDialog
      trigger={
        <Button variant="outline" size="sm">
          Disconnect
        </Button>
      }
      title={<>Disconnect {name}?</>}
      description={
        <>
          {name} loses access immediately: its next request fails, and it has to ask you again to reconnect. Sites it
          published stay as they are.
        </>
      }
      action="Disconnect"
      pending="Disconnecting…"
      onConfirm={async () => {
        await deleteGrant(grant.id)
        toast.success(<>Disconnected {name}.</>)
        await onDone()
      }}
    />
  )
}
