import { execFileSync } from "node:child_process"
import path from "node:path"

import type { BrowserContext } from "@playwright/test"

/**
 * Test-only seeding through internal/sites/e2eseed (a Go command, not a
 * server endpoint): it writes accounts, sessions, and published sites
 * straight into the database and storage the server under test uses, so it
 * needs the server's DATABASE_URL and storage variables in its environment
 * (mise.toml's [env] locally; the CI job sets them for the container).
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
}

export type SeedResult = {
  users: Record<string, { id: string; sessionToken: string }>
  sites: { id: string; versions: string[] }[]
}

const repoRoot = path.resolve(__dirname, "..", "..", "..")

export function seed(spec: SeedSpec): SeedResult {
  const out = execFileSync("go", ["run", "./internal/sites/e2eseed"], {
    cwd: repoRoot,
    input: JSON.stringify(spec),
    env: process.env,
    stdio: ["pipe", "pipe", "inherit"],
    timeout: 180_000,
  })
  return JSON.parse(out.toString()) as SeedResult
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
      url: baseURL,
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
