import { createHash, randomBytes } from "node:crypto"

import { runSeeder } from "./seeders"

/**
 * Registers the test CIMD client through internal/oauth/e2eseed (a Go
 * command, not a server endpoint), with the server's DATABASE_URL in the
 * environment like ./seed.ts.
 */
export function seedClient(): { clientId: string; redirectUri: string } {
  return JSON.parse(runSeeder("oauth")) as { clientId: string; redirectUri: string }
}

/** A PKCE verifier and its S256 challenge. */
export function pkce(): { verifier: string; challenge: string } {
  const verifier = randomBytes(32).toString("base64url")
  const challenge = createHash("sha256").update(verifier).digest("base64url")
  return { verifier, challenge }
}
