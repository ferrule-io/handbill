"use client"

import {
  startAuthentication,
  startRegistration,
  type PublicKeyCredentialCreationOptionsJSON,
  type PublicKeyCredentialRequestOptionsJSON,
} from "@simplewebauthn/browser"

import { api, refreshMe } from "@/lib/api"

/** Client halves of the sign-in ceremonies served by internal/auth. */

type Begin<O> = { flow: string; options: O }

export type Passkey = {
  id: string
  name: string
  synced: boolean
  attachment: string
  cloneWarning: boolean
  createdAt: string
  lastUsedAt: string | null
}

/** Creates an account for email with a new passkey and signs it in. */
export async function signUpWithPasskey(email: string) {
  const begin = await api<Begin<PublicKeyCredentialCreationOptionsJSON>>("/app-api/auth/passkey/signup/begin", {
    method: "POST",
    body: { email },
  })
  const credential = await startRegistration({ optionsJSON: begin.options })
  await api("/app-api/auth/passkey/signup/finish", { method: "POST", body: { flow: begin.flow, credential } })
  await refreshMe()
}

/** Signs in with a passkey: usernameless, or limited to email's accounts. */
export async function signInWithPasskey(email?: string) {
  const begin = await api<Begin<PublicKeyCredentialRequestOptionsJSON>>("/app-api/auth/passkey/login/begin", {
    method: "POST",
    body: { email: email ?? "" },
  })
  const credential = await startAuthentication({ optionsJSON: begin.options })
  await api("/app-api/auth/passkey/login/finish", { method: "POST", body: { flow: begin.flow, credential } })
  await refreshMe()
}

/**
 * Registers another passkey for the signed-in account. From a recovery
 * session this also upgrades it to a normal session (upgraded: true).
 */
export async function addPasskey(name?: string) {
  const begin = await api<Begin<PublicKeyCredentialCreationOptionsJSON>>("/app-api/auth/passkey/register/begin", {
    method: "POST",
  })
  const credential = await startRegistration({ optionsJSON: begin.options })
  const out = await api<{ passkey: Passkey; upgraded: boolean }>("/app-api/auth/passkey/register/finish", {
    method: "POST",
    body: { flow: begin.flow, credential, ...(name ? { name } : {}) },
  })
  if (out.upgraded) await refreshMe()
  return out
}

/** Sends the browser to Google; it comes back through /auth/google/callback. */
export async function startGoogle(mode: "signin" | "link", returnTo?: string | null) {
  const { url } = await api<{ url: string }>("/app-api/auth/google/start", {
    method: "POST",
    body: { mode, return: returnTo ?? "" },
  })
  window.location.assign(url)
}

export async function signOut() {
  await api("/app-api/auth/signout", { method: "POST" })
  await refreshMe()
}

/** Messages for the ?error= codes the Google callback redirects with. */
export const googleErrors: Record<string, string> = {
  google_cancelled: "Google sign-in was cancelled.",
  google_expired: "That Google sign-in took too long or was started elsewhere. Try again.",
  google_failed: "Google couldn't confirm who you are. Try again.",
  google_disabled: "Google sign-in isn't enabled on this server.",
  email_in_use:
    "An account already uses that Google email. Sign in with its passkey, then link Google from Settings → Security.",
  google_linked_elsewhere: "That Google account is already linked to a different agent-dash account.",
  google_already_linked: "A Google account is already linked. Unlink it first to link another.",
}
