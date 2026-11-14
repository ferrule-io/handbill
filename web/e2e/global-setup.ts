import { mkdtempSync, rmSync } from "node:fs"
import os from "node:os"
import path from "node:path"

import type { FullConfig } from "@playwright/test"

import { seed, siteOrigin } from "./support/seed"
import { binDirEnv, buildSeeders } from "./support/seeders"

/**
 * Builds the Go seeders once, then proves they write where the server under
 * test reads: a probe site is seeded with a public link and fetched through
 * the server. A server on another database answers 404, one on other
 * storage 500 ("sites: not found"); either stops the run here, before specs
 * fail in confusing ways.
 */
export default async function globalSetup(config: FullConfig) {
  const dir = mkdtempSync(path.join(os.tmpdir(), "handbill-e2e-"))
  process.env[binDirEnv] = dir // inherited by the workers
  buildSeeders(dir)

  const baseURL = config.projects[0].use.baseURL ?? "http://dash.localhost:8080"
  const { probe } = seed({ users: [], probe: true })
  if (!probe) throw new Error("e2eseed returned no probe")
  const origin = siteOrigin(baseURL, probe.site)
  const share = await fetch(`${origin}/_auth/share/${probe.token}`, { redirect: "manual" })
  const cookie = share.headers
    .getSetCookie()
    .map((c) => c.split(";")[0])
    .find((c) => c.startsWith("__Host-site="))
  const page = cookie ? await fetch(origin + probe.path, { headers: { cookie }, redirect: "manual" }) : undefined
  const body = page ? await page.text() : ""
  if (page?.status !== 200 || body !== probe.body) {
    const env = ["DATABASE_URL", "PGHOST", "PGDATABASE", "STORAGE_TYPE", "STORAGE_LOCAL_DIR", "S3_BUCKET", "S3_ENDPOINT", "GCS_BUCKET"]
      .filter((k) => process.env[k])
      .map((k) => `  ${k}=${k === "DATABASE_URL" ? process.env[k]!.replace(/:[^:@/]*@/, ":***@") : process.env[k]}`)
      .join("\n")
    throw new Error(
      `The e2e seeder and the server under test (${baseURL}) do not share a database and storage: ` +
        `share link answered ${share.status}, probe file answered ${page?.status ?? "-"} ${JSON.stringify(body.slice(0, 200))}.\n` +
        `Run Playwright with exactly the server's database and storage variables. The seeder used:\n${env}`
    )
  }
  return () => rmSync(dir, { recursive: true, force: true })
}
