import { expect, test, type APIRequestContext, type Page } from "@playwright/test"

import { pkce, seedClient } from "./support/oauth"
import { seed, signIn, uniqueEmail } from "./support/seed"

// MCP authorization end to end (docs/roadmap.md step 5): an MCP client's
// authorization request, the consent screen, the code exchange, a tools
// call with the access token, and disconnecting from settings.

const REDIRECT = "http://127.0.0.1:9/callback"

let client: { clientId: string }
let user: { email: string; session: string }

test.beforeAll(() => {
  test.setTimeout(240_000)
  client = seedClient()
  const email = uniqueEmail("oauth")
  const out = seed({ users: [{ email }] })
  user = { email, session: out.users[email].sessionToken }
})

function authorizeURL(challenge: string) {
  const q = new URLSearchParams({
    client_id: client.clientId,
    redirect_uri: REDIRECT,
    response_type: "code",
    code_challenge: challenge,
    code_challenge_method: "S256",
    state: "s1",
  })
  return `/oauth/authorize?${q}`
}

/**
 * Opens the consent screen. The callback resolves with the client's
 * redirect URL (wrapped: an async function would flatten a bare promise).
 */
async function startConsent(page: Page, challenge: string) {
  const { promise: callback, resolve } = Promise.withResolvers<URL>()
  await page.route("http://127.0.0.1:9/**", async (route) => {
    resolve(new URL(route.request().url()))
    await route.fulfill({ status: 200, contentType: "text/plain", body: "callback" })
  })
  await page.goto(authorizeURL(challenge))
  await expect(page).toHaveURL(/\/consent\?request=/)
  return { callback }
}

async function mcp(request: APIRequestContext, token: string, method: string, params: object = {}) {
  return request.post("/mcp", {
    headers: {
      Authorization: `Bearer ${token}`,
      Accept: "application/json, text/event-stream",
      "MCP-Protocol-Version": "2025-11-25",
    },
    data: { jsonrpc: "2.0", id: 1, method, params },
  })
}

test("an MCP client is approved, calls tools, and is disconnected", async ({ page, context, baseURL, request }) => {
  await signIn(context, baseURL!, user.session)
  const { verifier, challenge } = pkce()
  const { callback } = await startConsent(page, challenge)

  const card = page.getByTestId("consent-card")
  await expect(card.getByRole("heading", { level: 1 })).toContainText("E2E Agent")
  await expect(card.getByRole("heading", { level: 1 })).toContainText(user.email)
  await expect(page.getByTestId("consent-client-host")).toHaveText("client.e2e.test")
  await expect(page.getByTestId("consent-loopback")).toContainText("127.0.0.1:9")
  await expect(page.getByTestId("consent-unverified")).toHaveCount(0)

  await page.getByRole("button", { name: "Allow" }).click()
  const back = await callback
  const code = back.searchParams.get("code")
  expect(code).toBeTruthy()
  expect(back.searchParams.get("state")).toBe("s1")
  expect(back.searchParams.get("iss")).toBe(new URL(baseURL!).origin)

  const res = await request.post("/oauth/token", {
    form: {
      grant_type: "authorization_code",
      code: code!,
      redirect_uri: REDIRECT,
      code_verifier: verifier,
      client_id: client.clientId,
    },
  })
  expect(res.status()).toBe(200)
  const tokens = (await res.json()) as { access_token: string; refresh_token: string }

  const init = await mcp(request, tokens.access_token, "initialize", {
    protocolVersion: "2025-11-25",
    capabilities: {},
    clientInfo: { name: "e2e", version: "1" },
  })
  expect(init.status()).toBe(200)
  const list = await mcp(request, tokens.access_token, "tools/list")
  expect(list.status()).toBe(200)
  const { result } = (await list.json()) as { result: { tools: { name: string }[] } }
  expect(result.tools).toHaveLength(15)

  await page.goto("/settings/security")
  const grants = page.getByTestId("oauth-grants")
  await expect(grants).toContainText("E2E Agent")
  await expect(grants).toContainText("client.e2e.test")
  await grants.getByRole("button", { name: "Disconnect" }).first().click()
  await page.getByRole("alertdialog").getByRole("button", { name: "Disconnect" }).click()
  await expect(page.getByText("Disconnected E2E Agent.")).toBeVisible()

  expect((await mcp(request, tokens.access_token, "tools/list")).status()).toBe(401)
})

test("denying sends the client access_denied", async ({ page, context, baseURL }) => {
  await signIn(context, baseURL!, user.session)
  const { callback } = await startConsent(page, pkce().challenge)
  await page.getByRole("button", { name: "Cancel" }).click()
  const back = await callback
  expect(back.searchParams.get("error")).toBe("access_denied")
  expect(back.searchParams.get("state")).toBe("s1")
})

test("a signed-out browser signs in first", async ({ page }) => {
  await page.goto(authorizeURL(pkce().challenge))
  await expect(page).toHaveURL(/\/login\?return=%2Foauth%2Fauthorize/)
})

test("an invalid client gets the error card", async ({ page }) => {
  await page.goto("/consent?error=client_metadata_invalid")
  const card = page.getByTestId("consent-error")
  await expect(card.getByRole("heading", { level: 1 })).toHaveText("This connection can’t continue")
  await expect(card).toContainText("Go back to your app and try connecting again.")
})
