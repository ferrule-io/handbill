"use client"

import { CheckIcon, CornerDownLeftIcon, ShieldAlertIcon, TerminalIcon } from "lucide-react"
import { useSearchParams } from "next/navigation"
import { useEffect, useState } from "react"

import { AuthHeading, FormError, Spinner } from "@/components/auth/parts"
import { ConsentError } from "@/components/consent/consent-error"
import { clientName, decide, getConsent, type ConsentRequest } from "@/components/consent/data"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { ApiError, errorMessage, loginHref } from "@/lib/api"

const EXPIRED = "This request expired or belongs to another account. Start the connection again from your app."

/**
 * /consent?request=<handle> asks the signed-in user to approve an MCP
 * client; /consent?error=<code> explains why a request can't continue.
 */
export function Consent() {
  const params = useSearchParams()
  const request = params.get("request")
  const error = params.get("error")
  const [data, setData] = useState<ConsentRequest | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)

  useEffect(() => {
    if (!request || error) return
    getConsent(request).then(
      (r) => setData(r.request),
      (err) => {
        if (err instanceof ApiError && err.status === 401) {
          window.location.assign(loginHref("/consent?request=" + encodeURIComponent(request)))
        } else if (err instanceof ApiError && err.status === 404) {
          setLoadError(EXPIRED)
        } else {
          setLoadError(errorMessage(err))
        }
      }
    )
  }, [request, error])

  if (error || !request) return <ConsentError code={error ?? undefined} />
  if (loadError) return <ConsentError message={loadError} />
  if (!data) return <ConsentSkeleton />
  return <ConsentCard request={request} data={data} />
}

function ConsentSkeleton() {
  return (
    <div className="flex flex-col gap-6" aria-busy="true">
      <div className="flex flex-col gap-3">
        <Skeleton className="h-3 w-24" />
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-4 w-40" />
      </div>
      <Skeleton className="h-40 w-full rounded-2xl" />
      <Skeleton className="h-10 w-full" />
    </div>
  )
}

const abilities = [
  "Create sites you own",
  "Publish, update, and roll back sites you own or manage",
  "Read their files",
  "Change titles and descriptions",
  "Delete sites you own",
]

export function ConsentCard({ request, data }: { request: string; data: ConsentRequest }) {
  const [busy, setBusy] = useState<"allow" | "cancel" | null>(null)
  const [returning, setReturning] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const name = clientName(data.client)
  const { redirect } = data

  async function submit(approve: boolean) {
    setBusy(approve ? "allow" : "cancel")
    setError(null)
    try {
      const { redirectTo } = await decide(request, approve)
      setReturning(true)
      window.location.assign(redirectTo)
    } catch (err) {
      setError(err instanceof ApiError && err.status === 404 ? EXPIRED : errorMessage(err))
      setBusy(null)
    }
  }

  if (returning) {
    return (
      <div className="flex flex-col gap-4" role="status" data-testid="consent-returning">
        <AuthHeading eyebrow="Connect an app" title={<>Returning you to {name}…</>}>
          {redirect.loopback && "If nothing happens, you can close this tab."}
        </AuthHeading>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-6" data-testid="consent-card">
      <AuthHeading
        eyebrow="Connect an app"
        title={
          <>
            <span className="text-brand">{name}</span> wants to publish sites as{" "}
            <span className="break-words">{data.account.email}</span>
          </>
        }
      >
        <p className="font-mono text-xs break-all text-foreground" data-testid="consent-client-host">
          {data.client.host}
        </p>
        <p className="mt-1">
          Sends you back to: <span className="font-mono text-xs break-all text-foreground">{redirect.host}</span>
        </p>
      </AuthHeading>

      <div className="rounded-2xl bg-card p-5 ring-1 ring-border">
        <p className="eyebrow">It will be able to</p>
        <ul className="mt-3 flex flex-col gap-2 text-sm">
          {abilities.map((a) => (
            <li key={a} className="flex gap-2.5">
              <CheckIcon className="mt-0.5 size-4 shrink-0 text-brand" aria-hidden="true" />
              {a}
            </li>
          ))}
        </ul>
      </div>

      {(!data.client.verified || redirect.loopback || redirect.host !== data.client.host || data.previouslyAuthorized) && (
        <div className="flex flex-col gap-3">
          {!data.client.verified && (
            <Alert data-testid="consent-unverified">
              <ShieldAlertIcon />
              <AlertTitle>Unverified app.</AlertTitle>
              <AlertDescription>
                This app registered itself; its name was not checked. Only continue if you just started this
                connection yourself.
              </AlertDescription>
            </Alert>
          )}
          {redirect.loopback ? (
            <Alert data-testid="consent-loopback">
              <TerminalIcon />
              <AlertTitle>A program on this computer</AlertTitle>
              <AlertDescription>
                This connection hands access to a program on this computer ({redirect.host}). Only continue if you
                just started connecting {name} here.
              </AlertDescription>
            </Alert>
          ) : (
            redirect.host !== data.client.host && (
              <Alert>
                <CornerDownLeftIcon />
                <AlertDescription>
                  After you approve, you’ll be sent to <strong className="text-foreground">{redirect.host}</strong>.
                </AlertDescription>
              </Alert>
            )
          )}
          {data.previouslyAuthorized && (
            <p className="text-sm text-muted-foreground">
              You’ve connected {name} before: approving again adds a new connection.
            </p>
          )}
        </div>
      )}

      <FormError>{error}</FormError>

      <div className="flex flex-col gap-2.5 sm:flex-row-reverse">
        <Button variant="brand" size="lg" className="sm:flex-1" onClick={() => submit(true)} disabled={busy !== null}>
          {busy === "allow" && <Spinner />} Allow
        </Button>
        <Button variant="outline" size="lg" className="sm:flex-1" onClick={() => submit(false)} disabled={busy !== null}>
          {busy === "cancel" && <Spinner />} Cancel
        </Button>
      </div>
    </div>
  )
}
