import { expect, test, type Page } from "@playwright/test"

import { seed, signIn, siteOrigin, uniqueEmail } from "./support/seed"

// Routing acceptance check (docs/design.md, "Frontend"): fixed routes with
// ids created at runtime must render on direct load and on client-side
// <Link> navigation; unknown paths get the export's 404; site hosts never
// serve the UI.

const BASE32 = "abcdefghijklmnopqrstuvwxyz234567"

function randomSiteId(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(12))
  return Array.from(bytes, (b) => BASE32[b % 32]).join("")
}

function randomToken(): string {
  return Buffer.from(crypto.getRandomValues(new Uint8Array(32))).toString("base64url")
}

/** Marks the current document so a full page load can be detected. */
async function markDocument(page: Page) {
  await page.evaluate(() => ((window as unknown as { __spa: boolean }).__spa = true))
}

async function expectSameDocument(page: Page) {
  expect(await page.evaluate(() => (window as unknown as { __spa?: boolean }).__spa)).toBe(true)
}

// A site created at runtime, and its owner's session.
let id: string
let session: string

test.beforeAll(async () => {
  test.setTimeout(180_000)
  const owner = uniqueEmail("routing")
  const out = seed({ users: [{ email: owner }], sites: [{ owner, title: "Routing check" }] })
  id = out.sites[0].id
  session = out.users[owner].sessionToken
})

test.beforeEach(async ({ page, context, baseURL }) => {
  page.on("pageerror", (err) => {
    throw err
  })
  await signIn(context, baseURL!, session)
})

test("direct load of /site?id=… renders the runtime id", async ({ page }) => {
  const res = await page.goto(`/site?id=${id}`)
  expect(res?.status()).toBe(200)
  await expect(page.getByTestId("site-id")).toHaveText(id)
  await expect(page.getByTestId("site-title")).toHaveText("Routing check")
})

test("<Link> navigation to /site?id=… renders the runtime id", async ({ page }) => {
  await page.goto("/")
  await markDocument(page)
  await page.getByLabel("Site id").fill(id)
  await page.getByTestId("open-site").click()
  await expect(page).toHaveURL(`/site?id=${id}`)
  await expect(page.getByTestId("site-id")).toHaveText(id)
  await expectSameDocument(page)
})

test("direct load of /invite#token=… renders the runtime token", async ({ page }) => {
  const token = randomToken()
  const res = await page.goto(`/invite#token=${token}`)
  expect(res?.status()).toBe(200)
  await expect(page.getByTestId("invite-token-tail")).toHaveText(`…${token.slice(-6)}`)
})

test("<Link> navigation to /invite#token=… renders the runtime token", async ({ page, baseURL }) => {
  const token = randomToken()
  await page.goto("/")
  await markDocument(page)
  await page.getByLabel("Invite link").fill(`${baseURL}/invite#token=${token}`)
  await page.getByTestId("open-invite").click()
  await expect(page).toHaveURL(`/invite#token=${token}`)
  await expect(page.getByTestId("invite-token-tail")).toHaveText(`…${token.slice(-6)}`)
  await expectSameDocument(page)
})

test("client navigation between fixed routes keeps the document", async ({ page }) => {
  await page.goto(`/site?id=${id}`)
  await markDocument(page)
  await page.getByRole("link", { name: "Security" }).click()
  await expect(page).toHaveURL("/settings/security")
  await expect(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible()
  await page.goBack()
  await expect(page.getByTestId("site-id")).toHaveText(id)
  await expectSameDocument(page)
})

test("unknown paths return the export's 404 page with status 404", async ({ page }) => {
  for (const path of ["/no-such-page", "/site/extra", "/settings"]) {
    const res = await page.goto(path)
    expect(res?.status(), path).toBe(404)
    await expect(page.getByTestId("not-found")).toBeVisible()
  }
})

test("site hosts do not serve the UI", async ({ request, baseURL }) => {
  // Without a site cookie every path, existing site or not, is sent to the
  // app's sign-in handoff; nothing of the UI is served from a site host.
  for (const siteId of [id, randomSiteId()]) {
    const origin = siteOrigin(baseURL!, siteId)
    for (const path of ["/", "/site", "/_next/static/", "/404.html", "/healthz"]) {
      const res = await request.get(origin + path, { maxRedirects: 0 })
      expect(res.status(), path).toBe(302)
      const loc = new URL(res.headers()["location"])
      expect(loc.origin + loc.pathname, path).toBe(`${baseURL}/auth/site`)
      expect(loc.searchParams.get("site")).toBe(siteId)
      expect(loc.searchParams.get("return")).toBe(path)
      expect(await res.text()).not.toContain("__next")
    }
    const reserved = await request.get(origin + "/_auth/anything", { maxRedirects: 0 })
    expect(reserved.status()).toBe(404)
  }
})

test("every page runs under its CSP without violations", async ({ page }) => {
  // The CSP allows only each page's own inline scripts, by hash; a missing
  // hash would block Next's bootstrap and leave the page unhydrated.
  // Violations are reported to the test as they happen, so ones raised by a
  // page that then navigates away are kept too.
  const violations: string[] = []
  await page.exposeFunction("__reportCSP", (v: string) => violations.push(v))
  await page.addInitScript(() => {
    // exposeFunction installs __reportCSP on every document.
    const w: Window & { __reportCSP?: (v: string) => void } = window
    document.addEventListener("securitypolicyviolation", (e) =>
      w.__reportCSP?.(`${location.pathname}: ${e.violatedDirective} ${e.blockedURI}`)
    )
  })
  const consoleViolations: string[] = []
  page.on("console", (msg) => {
    if (msg.text().includes("Content Security Policy")) consoleViolations.push(msg.text())
  })
  // [path, where a signed-in visitor ends up]. The sign-in pages send a
  // signed-in visitor on with a full-page navigation (location.replace)
  // once /app-api/me answers; waiting for it keeps that navigation from
  // destroying the page under a later check or aborting the next goto.
  const paths: [string, string][] = [
    [`/site?id=${id}`, `/site?id=${id}`],
    ["/", "/"],
    ["/settings/security", "/settings/security"],
    ["/login", "/"],
    ["/login/recover", "/"],
    ["/signup", "/"],
    ["/invite", "/invite"],
    ["/verify-email", "/verify-email"],
    ["/consent?error=invalid_request", "/consent?error=invalid_request"],
    ["/no-such-page", "/no-such-page"],
  ]
  for (const [path, lands] of paths) {
    const res = await page.goto(path)
    const csp = res?.headers()["content-security-policy"] ?? ""
    expect(csp, path).toContain("frame-ancestors 'none'")
    expect(csp, path).toMatch(/script-src 'self'( 'sha256-[A-Za-z0-9+/=]+')+;/)
    await expect(page, path).toHaveURL(lands)
    // Hydrated: Next's router is live.
    await page.waitForFunction(() => "next" in window)
  }
  expect(violations).toEqual([])
  expect(consoleViolations).toEqual([])
})
