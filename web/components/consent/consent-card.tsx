"use client"

import { CheckIcon, CornerDownLeftIcon, MinusIcon, PlugIcon, ShieldAlertIcon, TerminalIcon } from "lucide-react"
import { useSearchParams } from "next/navigation"
import { useEffect, useState } from "react"

import { AuthHeading } from "@/components/auth/parts"
import { ConsentError } from "@/components/consent/consent-error"
import { clientName, decide, getConsent, type ConsentRequest } from "@/components/consent/data"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { FormError } from "@/components/ui/form-error"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
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
    <div className="flex flex-col gap-6" aria-busy="true" aria-label="Loading the connection request">
      <div className="flex flex-col gap-2.5">
        <Skeleton className="mb-2 size-11 rounded-xl" />
        <Skeleton className="h-4 w-28" />
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-9 w-2/3" />
        <Skeleton className="h-4 w-40" />
      </div>
      <Skeleton className="h-56 w-full rounded-2xl" />
      <Skeleton className="h-11 w-full rounded-xl" />
    </div>
  )
}

const can = [
  "Create sites, and publish, update, or roll back the ones you own or manage",
  "Read the files of those sites, and change their titles and descriptions",
  "Invite people, change or remove their access, and turn public links on or off",
  "Delete or hand over sites you own",
]

const cannot = [
  "Open sites that were only shared with you to view",
  "Sign in to Handbill as you, or change your passkeys and settings",
]

/**
 * True once the page has been visible and focused for `delay` ms without
 * interruption; hiding the tab or blurring the window starts over. Keeps
 * Allow inert so a window popped up under a double-click can't land on it.
 */
function useSettled(delay: number) {
  const [settled, setSettled] = useState(false)
  useEffect(() => {
    let timer: number | undefined
    const restart = () => {
      window.clearTimeout(timer)
      setSettled(false)
      if (document.visibilityState === "visible" && document.hasFocus()) {
        timer = window.setTimeout(() => setSettled(true), delay)
      }
    }
    restart()
    document.addEventListener("visibilitychange", restart)
    window.addEventListener("focus", restart)
    window.addEventListener("blur", restart)
    return () => {
      window.clearTimeout(timer)
      document.removeEventListener("visibilitychange", restart)
      window.removeEventListener("focus", restart)
      window.removeEventListener("blur", restart)
    }
  }, [delay])
  return settled
}

export function ConsentCard({ request, data }: { request: string; data: ConsentRequest }) {
  const [busy, setBusy] = useState<"allow" | "cancel" | null>(null)
  const [returning, setReturning] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const settled = useSettled(500)
  // Client-supplied strings are isolated so bidi controls in them can't
  // reorder the text around them.
  const name = <bdi>{clientName(data.client)}</bdi>
  const { redirect } = data
  const redirectHost = <bdi>{redirect.host}</bdi>

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
        <AuthHeading icon={<Spinner />} tone="muted" eyebrow="Connect an app" title={<>Returning you to {name}…</>}>
          {redirect.loopback && <p>If nothing happens in a few seconds, you can close this tab.</p>}
        </AuthHeading>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-6" data-testid="consent-card">
      <AuthHeading
        icon={<PlugIcon />}
        eyebrow="Connect an app"
        title={
          <>
            <span className="text-brand-strong">{name}</span> wants to publish sites as{" "}
            <span className="break-words">{data.account.email}</span>
          </>
        }
      >
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
          <dt>App</dt>
          <dd className="font-mono text-xs leading-5 break-all text-foreground" data-testid="consent-client-host">
            <bdi>{data.client.host}</bdi>
          </dd>
          <dt>Returns to</dt>
          <dd className="font-mono text-xs leading-5 break-all text-foreground" data-testid="consent-redirect-host">
            {redirectHost}
          </dd>
        </dl>
      </AuthHeading>

      {(!data.client.verified || redirect.loopback || redirect.host !== data.client.host) && (
        <div className="flex flex-col gap-3">
          {!data.client.verified && (
            <Alert variant="warning" data-testid="consent-unverified">
              <ShieldAlertIcon />
              <AlertTitle>Handbill can’t confirm who this is</AlertTitle>
              <AlertDescription>
                This app registered itself, so its name wasn’t checked. Only allow it if you started this connection
                yourself, just now.
              </AlertDescription>
            </Alert>
          )}
          {redirect.loopback ? (
            <Alert data-testid="consent-loopback">
              <TerminalIcon />
              <AlertTitle>An app on this computer</AlertTitle>
              <AlertDescription>
                Access goes to a program running on this computer ({redirectHost}), as it does for desktop and
                command-line agents. Only allow it if you just started connecting {name} here.
              </AlertDescription>
            </Alert>
          ) : (
            redirect.host !== data.client.host && (
              <Alert>
                <CornerDownLeftIcon />
                <AlertDescription>
                  After you allow, you’ll be sent to <strong className="text-foreground">{redirectHost}</strong>.
                </AlertDescription>
              </Alert>
            )
          )}
        </div>
      )}

      <div className="flex flex-col gap-4 rounded-2xl bg-card p-5 ring-1 ring-border">
        <div className="flex flex-col gap-3">
          <p className="eyebrow">It will be able to</p>
          <ul className="flex flex-col gap-2 text-sm">
            {can.map((a) => (
              <li key={a} className="flex gap-2.5">
                <CheckIcon className="mt-0.5 size-4 shrink-0 text-brand-strong" aria-hidden="true" />
                {a}
              </li>
            ))}
          </ul>
        </div>
        <div className="flex flex-col gap-3 border-t border-border pt-4">
          <p className="eyebrow">It won’t be able to</p>
          <ul className="flex flex-col gap-2 text-sm text-muted-foreground">
            {cannot.map((a) => (
              <li key={a} className="flex gap-2.5">
                <MinusIcon className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                {a}
              </li>
            ))}
          </ul>
        </div>
      </div>

      <p className="text-sm leading-relaxed text-muted-foreground">
        {data.previouslyAuthorized && <>You’ve connected {name} before; allowing again adds a new connection. </>}
        After you allow, you go back to {name}. You can disconnect it at any time in Settings → Security.
      </p>

      <FormError>{error}</FormError>

      <div className="flex flex-col gap-2.5 sm:flex-row-reverse">
        <Button
          variant="brand"
          size="xl"
          className="sm:flex-1"
          onClick={() => submit(true)}
          disabled={busy !== null || !settled}
        >
          {busy === "allow" && <Spinner data-icon="inline-start" />}
          {busy === "allow" ? "Allowing…" : "Allow"}
        </Button>
        <Button variant="outline" size="xl" className="sm:flex-1" onClick={() => submit(false)} disabled={busy !== null}>
          {busy === "cancel" && <Spinner data-icon="inline-start" />}
          Cancel
        </Button>
      </div>
    </div>
  )
}
