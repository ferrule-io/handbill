import { execFileSync } from "node:child_process"
import path from "node:path"

/**
 * The Go seeders (internal/sites/e2eseed, internal/oauth/e2eseed) are test
 * tools that write straight into the database and storage of the server
 * under test, so they must see exactly the server's DATABASE_URL / PG* and
 * STORAGE_* variables. They are built once by global-setup.ts and run as
 * plain binaries: `go run` goes through the mise shim, which replaces those
 * variables with mise.toml's [env] and silently seeds somewhere else.
 */

export const repoRoot = path.resolve(__dirname, "..", "..", "..")

export type Seeder = "sites" | "oauth"

const packages: Record<Seeder, string> = {
  sites: "./internal/sites/e2eseed",
  oauth: "./internal/oauth/e2eseed",
}

/** Set by global-setup.ts to the directory holding the built seeders. */
export const binDirEnv = "HANDBILL_E2E_SEEDER_DIR"

export function buildSeeders(dir: string) {
  for (const [name, pkg] of Object.entries(packages)) {
    execFileSync("go", ["build", "-o", path.join(dir, `${name}-e2eseed`), pkg], {
      cwd: repoRoot,
      stdio: ["ignore", "inherit", "inherit"],
      timeout: 300_000,
    })
  }
}

/** Runs a built seeder with this process's environment, unchanged. */
export function runSeeder(name: Seeder, input?: string): string {
  const dir = process.env[binDirEnv]
  if (!dir) throw new Error(`${binDirEnv} is unset: run the specs through playwright.config.ts (globalSetup builds the seeders)`)
  return execFileSync(path.join(dir, `${name}-e2eseed`), [], {
    env: process.env,
    input,
    stdio: [input === undefined ? "ignore" : "pipe", "pipe", "inherit"],
    timeout: 120_000,
  }).toString()
}
