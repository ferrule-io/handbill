import { api } from "@/lib/api"

/** A client as the consent screen and the grants list show it. */
export type OAuthClient = {
  /** The CIMD document URL, or a dynamically registered id. */
  clientId: string
  name: string
  /** The client_id URL's host (CIMD) or first redirect URI's host (DCR). */
  host: string
  /** CIMD clients are verified by their host; DCR clients named themselves. */
  verified: boolean
}

export type ConsentRequest = {
  client: OAuthClient
  redirect: { uri: string; host: string; loopback: boolean }
  account: { email: string }
  previouslyAuthorized: boolean
  expiresAt: string
}

export type Grant = {
  id: string
  client: OAuthClient
  createdAt: string
  lastUsedAt: string | null
}

/** The name to show for a client: its own, or its host. */
export function clientName(c: OAuthClient): string {
  return c.name || c.host
}

export function getConsent(request: string) {
  return api<{ request: ConsentRequest }>(`/app-api/oauth/consent?request=${encodeURIComponent(request)}`)
}

export function decide(request: string, approve: boolean) {
  return api<{ redirectTo: string }>("/app-api/oauth/consent", { method: "POST", body: { request, approve } })
}

export function listGrants() {
  return api<{ grants: Grant[] }>("/app-api/oauth/grants")
}

export function deleteGrant(id: string) {
  return api<void>(`/app-api/oauth/grants/${encodeURIComponent(id)}`, { method: "DELETE" })
}
