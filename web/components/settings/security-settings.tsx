"use client"

import {
  BadgeCheckIcon,
  CheckIcon,
  FingerprintIcon,
  KeyRoundIcon,
  LaptopIcon,
  MailIcon,
  PencilIcon,
  PlusIcon,
  RefreshCwIcon,
  ShieldAlertIcon,
  SmartphoneIcon,
  TriangleAlertIcon,
  UsbIcon,
  XIcon,
} from "lucide-react"
import { useSearchParams } from "next/navigation"
import { useCallback, useEffect, useState } from "react"
import { toast } from "sonner"

import { addPasskey, googleErrors, startGoogle, type Passkey } from "@/components/auth/ceremonies"
import { GoogleIcon } from "@/components/auth/google-icon"
import { RecoveryCodes } from "@/components/auth/recovery-codes"
import { Container, Stack } from "@/components/layout/container"
import { LoadError } from "@/components/layout/empty-state"
import { PageHeader, Section } from "@/components/layout/page-header"
import { OAuthGrants } from "@/components/settings/oauth-grants"
import { formatDate, relative, Row, RowSkeleton } from "@/components/settings/row"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { api, errorMessage, refreshMe, useMe } from "@/lib/api"

type Security = {
  emails: { email: string; verified: boolean; primary: boolean }[]
  passkeys: Passkey[]
  google: { email: string; linkedAt: string; lastUsedAt: string | null } | null
  recoveryCodes: { remaining: number; total: number; issuedAt: string | null }
  sessions: { id: string; device: string; current: boolean; createdAt: string; lastSeenAt: string }[]
}

/** /settings/security: passkeys, Google, recovery codes, sessions. */
export function SecuritySettings() {
  const params = useSearchParams()
  const [data, setData] = useState<Security | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)

  const reload = useCallback(async () => {
    try {
      setData(await api<Security>("/app-api/auth/security"))
      setLoadError(null)
    } catch (err) {
      setLoadError(errorMessage(err))
    }
  }, [])

  useEffect(() => {
    api<Security>("/app-api/auth/security").then(setData, (err) => setLoadError(errorMessage(err)))
  }, [])

  // Outcomes of redirects that land here (Google linking, recovery).
  useEffect(() => {
    const error = params.get("error")
    if (params.get("linked") === "google") toast.success("Google account linked.")
    else if (params.get("recovered")) toast.success("You’re back in. Review your passkeys and issue new recovery codes.")
    else if (error) toast.error(googleErrors[error] ?? "That didn’t complete. Try again.")
    else return
    history.replaceState(null, "", location.pathname)
  }, [params])

  return (
    <Container>
      <Stack gap="loose">
        <PageHeader
          eyebrow="Settings"
          title="Security"
          description="How you sign in, how you get back in, and which agents can publish as you."
        />
        {loadError && !data ? (
          <LoadError
            title="Couldn’t load your security settings"
            message={loadError}
            onRetry={() => {
              setLoadError(null)
              void reload()
            }}
          />
        ) : (
          <>
            <EmailSection data={data} />
            <PasskeysSection data={data} reload={reload} />
            <GoogleSection data={data} reload={reload} />
            <RecoverySection data={data} reload={reload} />
            <SessionsSection data={data} reload={reload} />
          </>
        )}
        <OAuthGrants />
      </Stack>
    </Container>
  )
}

type SectionProps = { data: Security | null; reload: () => Promise<void> }

function EmailSection({ data }: { data: Security | null }) {
  const { mailEnabled } = useMe()
  const [busy, setBusy] = useState(false)
  const email = data?.emails.find((e) => e.primary)
  async function send() {
    setBusy(true)
    try {
      await api("/app-api/auth/email/send-verification", { method: "POST" })
      toast.success(`Verification link sent to ${email?.email}.`)
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Section title="Email" description="Invitations to a verified address reach you without the link.">
      {!data ? (
        <RowSkeleton />
      ) : !email ? (
        <Row icon={<MailIcon />} title="No email address" detail="This account signs in with Google only." />
      ) : (
        <Row
          icon={<MailIcon />}
          title={<span className="font-mono text-[0.85rem] break-all">{email.email}</span>}
          badge={
            email.verified ? (
              <Badge variant="secondary" className="gap-1 text-success">
                <BadgeCheckIcon /> Verified
              </Badge>
            ) : (
              <Badge variant="outline">Unverified</Badge>
            )
          }
          detail={
            email.verified
              ? "Confirmed by a verification link or Google."
              : mailEnabled
                ? "Confirm it with a link sent to this address."
                : "This server doesn’t send email; linking a Google account with this address verifies it."
          }
          action={
            !email.verified &&
            mailEnabled && (
              <Button variant="outline" size="sm" onClick={send} disabled={busy}>
                {busy ? <Spinner /> : <MailIcon />}
                {busy ? "Sending…" : "Send link"}
              </Button>
            )
          }
        />
      )}
    </Section>
  )
}

function PasskeysSection({ data, reload }: SectionProps) {
  const [adding, setAdding] = useState(false)
  async function add() {
    setAdding(true)
    try {
      const { passkey } = await addPasskey()
      toast.success(`Added “${passkey.name}”.`)
      await reload()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setAdding(false)
    }
  }
  const methods = (data?.passkeys.length ?? 0) + (data?.google ? 1 : 0)
  return (
    <Section
      title="Passkeys"
      description="Sign in with your device. Add one per device, or use a synced passkey from your password manager."
      actions={
        <Button variant="outline" size="sm" onClick={add} disabled={adding || !data}>
          {adding ? <Spinner /> : <PlusIcon />}
          {adding ? "Waiting for your passkey…" : "Add passkey"}
        </Button>
      }
    >
      {!data ? (
        <RowSkeleton />
      ) : data.passkeys.length === 0 ? (
        <Row icon={<FingerprintIcon />} title="No passkeys yet" detail="Add one to sign in without Google." />
      ) : (
        <ul className="flex flex-col gap-3" data-testid="passkey-list">
          {data.passkeys.map((p) => (
            <PasskeyRow key={p.id} passkey={p} canRemove={methods > 1} reload={reload} />
          ))}
        </ul>
      )}
    </Section>
  )
}

function PasskeyRow({ passkey, canRemove, reload }: { passkey: Passkey; canRemove: boolean; reload: () => Promise<void> }) {
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState(passkey.name)
  const [busy, setBusy] = useState(false)

  async function rename(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await api(`/app-api/auth/passkeys/${passkey.id}`, { method: "PATCH", body: { name } })
      setEditing(false)
      await reload()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const icon =
    passkey.attachment === "cross-platform" && !passkey.synced ? <UsbIcon /> : passkey.synced ? <KeyRoundIcon /> : <FingerprintIcon />
  const meta = [
    passkey.synced ? "Synced" : passkey.attachment === "cross-platform" ? "Security key" : "This device only",
    `added ${formatDate(passkey.createdAt)}`,
    passkey.lastUsedAt ? `last used ${relative(passkey.lastUsedAt)}` : "never used to sign in",
    ...(canRemove ? [] : ["your only way to sign in"]),
  ].join(" · ")

  return (
    <li>
      <Row
        icon={icon}
        title={
          editing ? (
            <form onSubmit={rename} className="flex w-full items-center gap-1.5">
              <Input
                aria-label="Passkey name"
                value={name}
                maxLength={64}
                autoFocus
                onChange={(e) => setName(e.target.value)}
                className="h-8 max-w-64"
                disabled={busy}
              />
              <Button type="submit" size="icon-sm" variant="outline" aria-label="Save name" disabled={busy || !name.trim()}>
                {busy ? <Spinner /> : <CheckIcon />}
              </Button>
              <Button
                type="button"
                size="icon-sm"
                variant="ghost"
                aria-label="Cancel renaming"
                disabled={busy}
                onClick={() => {
                  setEditing(false)
                  setName(passkey.name)
                }}
              >
                <XIcon />
              </Button>
            </form>
          ) : (
            <span data-testid="passkey-name">{passkey.name}</span>
          )
        }
        badge={
          passkey.cloneWarning && (
            <Badge variant="destructive" className="gap-1">
              <ShieldAlertIcon /> Possibly copied
            </Badge>
          )
        }
        detail={meta}
        action={
          !editing && (
            <div className="flex items-center gap-1">
              <Button size="sm" variant="ghost" onClick={() => setEditing(true)} aria-label={`Rename ${passkey.name}`}>
                <PencilIcon /> Rename
              </Button>
              {canRemove && (
                <ConfirmDialog
                  trigger={
                    <Button size="sm" variant="ghost" className="text-destructive hover:text-destructive" aria-label={`Remove ${passkey.name}`}>
                      Remove
                    </Button>
                  }
                  title={`Remove “${passkey.name}”?`}
                  description="You won’t be able to sign in with it any more. Sites and other passkeys aren’t affected."
                  action="Remove passkey"
                  pending="Removing…"
                  onConfirm={async () => {
                    await api(`/app-api/auth/passkeys/${passkey.id}`, { method: "DELETE" })
                    toast.success(`Removed “${passkey.name}”.`)
                    await reload()
                  }}
                />
              )}
            </div>
          )
        }
      />
    </li>
  )
}

function GoogleSection({ data, reload }: SectionProps) {
  const { googleEnabled } = useMe()
  const [linking, setLinking] = useState(false)
  const onlyMethod = !!data?.google && data.passkeys.length === 0

  async function link() {
    setLinking(true)
    try {
      await startGoogle("link")
    } catch (err) {
      toast.error(errorMessage(err))
      setLinking(false)
    }
  }

  if (!googleEnabled && !data?.google) {
    return (
      <Section title="Google" description="A second way in, linked explicitly.">
        <Row icon={<GoogleIcon />} title="Not available" detail="Google sign-in isn’t set up on this server." />
      </Section>
    )
  }
  return (
    <Section title="Google" description="A second way in. Linking is explicit: signing in with Google never merges accounts.">
      {!data ? (
        <RowSkeleton />
      ) : data.google ? (
        <Row
          icon={<GoogleIcon />}
          title={<span className="font-mono text-[0.85rem] break-all">{data.google.email || "Google account"}</span>}
          badge={<Badge variant="secondary">Linked</Badge>}
          detail={[
            `Linked ${formatDate(data.google.linkedAt)}`,
            ...(data.google.lastUsedAt ? [`last used ${relative(data.google.lastUsedAt)}`] : []),
            ...(onlyMethod ? ["your only way to sign in, so add a passkey before unlinking"] : []),
          ].join(" · ")}
          action={
            !onlyMethod && (
              <ConfirmDialog
                trigger={
                  <Button size="sm" variant="outline">
                    Unlink
                  </Button>
                }
                title="Unlink your Google account?"
                description="You won’t be able to sign in with Google until you link it again. Your passkeys keep working."
                action="Unlink Google"
                pending="Unlinking…"
                onConfirm={async () => {
                  await api("/app-api/auth/google", { method: "DELETE" })
                  toast.success("Google account unlinked.")
                  await reload()
                }}
              />
            )
          }
        />
      ) : (
        <Row
          icon={<GoogleIcon />}
          title="Not linked"
          detail="Link a Google account to sign in with it too."
          action={
            <Button size="sm" variant="outline" onClick={link} disabled={linking}>
              {linking ? <Spinner /> : <GoogleIcon className="size-3.5" />} Link Google
            </Button>
          }
        />
      )}
    </Section>
  )
}

function RecoverySection({ data, reload }: SectionProps) {
  const { user } = useMe()
  const [codes, setCodes] = useState<string[] | null>(null)

  async function regenerate() {
    const r = await api<{ codes: string[] }>("/app-api/auth/recovery-codes", { method: "POST" })
    setCodes(r.codes)
    await reload()
  }

  const rc = data?.recoveryCodes
  const low = rc ? rc.remaining <= 3 : false
  return (
    <Section
      title="Recovery codes"
      description="Ten single-use codes for when every passkey is gone. Each opens a session that can only add a new passkey."
      actions={
        rc?.total ? (
          <ConfirmDialog
            trigger={
              <Button size="sm" variant="outline" disabled={!data}>
                <RefreshCwIcon /> Regenerate
              </Button>
            }
            tone="brand"
            title="Replace your recovery codes?"
            description="You get ten new codes, shown once. Every earlier code stops working, used or not."
            action="Replace codes"
            pending="Generating…"
            onConfirm={regenerate}
          />
        ) : (
          <GenerateCodes disabled={!data} onGenerate={regenerate} />
        )
      }
    >
      <div className="flex flex-col gap-4">
        {codes ? (
          <div className="flex flex-col gap-3">
            <RecoveryCodes codes={codes} email={user?.email} />
            <div className="flex items-center justify-between gap-3">
              <p className="text-xs text-muted-foreground">Save them now. They won’t be shown again.</p>
              <Button size="sm" variant="outline" onClick={() => setCodes(null)}>
                I’ve saved them
              </Button>
            </div>
          </div>
        ) : !rc ? (
          <RowSkeleton />
        ) : (
          <Row
            icon={low ? <TriangleAlertIcon /> : <KeyRoundIcon />}
            tone={low ? "warn" : undefined}
            title={
              rc.total === 0 ? (
                "No recovery codes"
              ) : (
                <span data-testid="recovery-remaining">
                  {rc.remaining} of {rc.total} left
                </span>
              )
            }
            detail={
              rc.total === 0
                ? "Generate a set so you can get back in if you lose your passkeys."
                : rc.remaining === 0
                  ? "Every code is used. Generate a new set."
                  : `Issued ${rc.issuedAt ? formatDate(rc.issuedAt) : ""}${low ? " · running low, generate a new set" : ""}`
            }
          />
        )}
      </div>
    </Section>
  )
}

function GenerateCodes({ disabled, onGenerate }: { disabled: boolean; onGenerate: () => Promise<void> }) {
  const [busy, setBusy] = useState(false)
  async function generate() {
    setBusy(true)
    try {
      await onGenerate()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Button size="sm" variant="outline" onClick={generate} disabled={disabled || busy}>
      {busy ? <Spinner /> : <RefreshCwIcon />}
      {busy ? "Generating…" : "Generate"}
    </Button>
  )
}

function SessionsSection({ data, reload }: SectionProps) {
  const [busy, setBusy] = useState(false)
  const others = data ? data.sessions.filter((s) => !s.current).length : 0
  async function revoke() {
    setBusy(true)
    try {
      const { revoked } = await api<{ revoked: number }>("/app-api/auth/sessions/revoke-others", { method: "POST" })
      toast.success(revoked === 1 ? "Signed out 1 other session." : `Signed out ${revoked} other sessions.`)
      await reload()
      await refreshMe()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Section
      title="Sessions"
      description="Browsers signed in to this account. Sessions end after 30 days without use."
      actions={
        <Button variant="outline" size="sm" onClick={revoke} disabled={busy || others === 0}>
          {busy && <Spinner />}
          {busy ? "Signing out…" : "Sign out other sessions"}
        </Button>
      }
    >
      {!data ? (
        <RowSkeleton />
      ) : (
        <ul className="flex flex-col gap-3">
          {data.sessions.map((s) => (
            <li key={s.id}>
              <Row
                icon={/iPhone|iPad|Android/.test(s.device) ? <SmartphoneIcon /> : <LaptopIcon />}
                title={s.device}
                badge={s.current && <Badge variant="secondary">This browser</Badge>}
                detail={`Signed in ${formatDate(s.createdAt)} · active ${relative(s.lastSeenAt)}`}
              />
            </li>
          ))}
        </ul>
      )}
    </Section>
  )
}
