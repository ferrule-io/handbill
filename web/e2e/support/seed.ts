import type { BrowserContext } from "@playwright/test"

import { runSeeder } from "./seeders"

/**
 * Test-only seeding through internal/sites/e2eseed (a Go command, not a
 * server endpoint): it writes accounts, sessions, and published sites
 * straight into the database and storage the server under test uses, so it
 * runs with this process's DATABASE_URL and storage variables, which must
 * be the server's (see ./seeders.ts; global-setup.ts checks they are).
 */

export type SeedFile = { path: string; text?: string; base64?: string }

export type SeedSpec = {
  users: { email: string }[]
  sites?: {
    owner: string
    title: string
    description?: string
    members?: { email: string; role: "manager" | "viewer" }[]
    /** Published in order (replace mode); the last one is live. */
    versions?: { files: SeedFile[] }[]
  }[]
  /** Also publish a public-link probe site (global-setup.ts). */
  probe?: boolean
}

export type SeedResult = {
  users: Record<string, { id: string; sessionToken: string }>
  sites: { id: string; versions: string[] }[]
  probe?: { site: string; token: string; path: string; body: string }
}

export function seed(spec: SeedSpec): SeedResult {
  return JSON.parse(runSeeder("sites", JSON.stringify(spec))) as SeedResult
}

/** A fresh address so repeated runs against one database never collide. */
export function uniqueEmail(label: string): string {
  return `${label}-${crypto.randomUUID().slice(0, 8)}@e2e.test`
}

/** Signs a browser context in by setting the app session cookie. */
export async function signIn(context: BrowserContext, baseURL: string, sessionToken: string) {
  await context.addCookies([
    {
      name: "__Host-session",
      value: sessionToken,
      // Domain without a leading dot is host-only, as __Host- requires;
      // Chromium rejects Secure cookies set through an http:// url.
      domain: new URL(baseURL).hostname,
      path: "/",
      secure: true,
      httpOnly: true,
      sameSite: "Lax",
    },
  ])
}

/** The origin of a site host, matching the app origin's scheme and port. */
export function siteOrigin(baseURL: string, siteId: string): string {
  const app = new URL(baseURL)
  const sitesDomain = process.env.SITES_DOMAIN ?? "sites.localhost"
  return `${app.protocol}//${siteId}.${sitesDomain}${app.port ? `:${app.port}` : ""}`
}
