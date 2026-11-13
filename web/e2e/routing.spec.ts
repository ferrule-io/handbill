import { expect, test, type Page } from "@playwright/test"

// Routing acceptance check (docs/design.md, "Frontend"): fixed routes with
// ids chosen at runtime must render on direct load and on client-side
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

test.beforeEach(({ page }) => {
  page.on("pageerror", (err) => {
    throw err
  })
})

test("direct load of /site?id=… renders the runtime id", async ({ page }) => {
  const id = randomSiteId()
  const res = await page.goto(`/site?id=${id}`)
  expect(res?.status()).toBe(200)
  await expect(page.getByTestId("site-id")).toHaveText(id)
})

test("<Link> navigation to /site?id=… renders the runtime id", async ({ page }) => {
  const id = randomSiteId()
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
  const id = randomSiteId()
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

test("site hosts do not serve the UI", async ({ page, baseURL }) => {
  const app = new URL(baseURL!)
  const sitesDomain = process.env.SITES_DOMAIN ?? "sites.localhost"
  const siteOrigin = `${app.protocol}//${randomSiteId()}.${sitesDomain}${app.port ? `:${app.port}` : ""}`
  for (const path of ["/", "/site", "/_next/static/", "/404.html"]) {
    const res = await page.goto(siteOrigin + path)
    expect(res?.status(), path).toBe(404)
    expect(await res?.text()).not.toContain("__next")
  }
})
