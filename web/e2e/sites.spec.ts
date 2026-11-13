import { expect, test, type Page } from "@playwright/test"

import { seed, signIn, siteOrigin, uniqueEmail, type SeedFile } from "./support/seed"

// Sites end to end (docs/roadmap.md step 4): a multi-page site with CSS,
// JS, and images, published at runtime, listed for its owner, opened on its
// own host through the sign-in handoff, rolled back from the UI, and
// invisible to another signed-in user.

// 1×1 PNG.
const PNG =
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

function version(label: string, color: string): { files: SeedFile[] } {
  const page = (title: string, body: string) => `<!doctype html>
<html><head><meta charset="utf-8"><title>${title}</title>
<link rel="stylesheet" href="/css/site.css"><script src="/js/app.js" defer></script></head>
<body><nav><a href="/">Home</a> <a href="/about">About</a></nav>
<h1>${title}</h1>${body}<p id="js">no script</p></body></html>`
  return {
    files: [
      { path: "index.html", text: page(`Itinerary ${label}`, `<img id="logo" src="img/logo.png" alt="logo">`) },
      { path: "about.html", text: page(`About ${label}`, "<p>Planned by an agent.</p>") },
      { path: "css/site.css", text: `h1 { color: ${color}; }` },
      { path: "js/app.js", text: `document.getElementById("js").textContent = "script ${label}"` },
      { path: "img/logo.png", base64: PNG },
      { path: "404.html", text: "<h1>Lost</h1>" },
    ],
  }
}

let siteId: string
let owner: { email: string; session: string }
let stranger: { email: string; session: string }

test.beforeAll(() => {
  test.setTimeout(180_000)
  const ownerEmail = uniqueEmail("owner")
  const strangerEmail = uniqueEmail("stranger")
  const out = seed({
    users: [{ email: ownerEmail }, { email: strangerEmail }],
    sites: [
      {
        owner: ownerEmail,
        title: "Kyoto in April",
        description: "Three itineraries for the spring trip.",
        versions: [version("one", "rgb(200, 30, 30)"), version("two", "rgb(30, 30, 200)")],
      },
    ],
  })
  siteId = out.sites[0].id
  owner = { email: ownerEmail, session: out.users[ownerEmail].sessionToken }
  stranger = { email: strangerEmail, session: out.users[strangerEmail].sessionToken }
})

async function expectVersion(page: Page, label: string, color: string) {
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(`Itinerary ${label}`)
  await expect(page.locator("#js")).toHaveText(`script ${label}`)
  await expect(page.getByRole("heading", { level: 1 })).toHaveCSS("color", color)
  await expect
    .poll(() => page.locator("#logo").evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth))
    .toBe(1)
}

test("owner opens the site through the handoff, browses it, and rolls back", async ({ page, context, baseURL }) => {
  await signIn(context, baseURL!, owner.session)
  const origin = siteOrigin(baseURL!, siteId)

  // The site is listed on the home page.
  await page.goto("/")
  const card = page.getByTestId("site-list").getByRole("link", { name: "Kyoto in April" })
  await expect(card).toBeVisible()
  await card.click()
  await expect(page).toHaveURL(`/site?id=${siteId}`)
  await expect(page.getByTestId("site-title")).toHaveText("Kyoto in April")
  await expect(page.getByTestId("version-row")).toHaveCount(2)
  await expect(page.getByTestId("current-version")).toContainText(owner.email)

  // "Open site" goes through /auth/site and /_auth/callback to the site host.
  const [site] = await Promise.all([context.waitForEvent("page"), page.getByTestId("open-site-link").click()])
  await site.waitForLoadState()
  await expect(site).toHaveURL(`${origin}/`)
  await expectVersion(site, "two", "rgb(30, 30, 200)")

  // Pages and assets resolve on the site host.
  await site.getByRole("link", { name: "About" }).click()
  await expect(site).toHaveURL(`${origin}/about`)
  await expect(site.getByRole("heading", { level: 1 })).toHaveText("About two")
  await expect(site.locator("#js")).toHaveText("script two")
  const missing = await site.goto(`${origin}/no/such/page`)
  expect(missing?.status()).toBe(404)
  await expect(site.getByRole("heading", { level: 1 })).toHaveText("Lost")

  // A deep link from a fresh tab returns to the requested path after the handoff.
  const deep = await context.newPage()
  await deep.goto(`${origin}/about?tab=2`)
  await expect(deep).toHaveURL(`${origin}/about?tab=2`)
  await expect(deep.getByRole("heading", { level: 1 })).toHaveText("About two")
  await deep.close()

  // Roll back to the first version from the UI.
  await page.locator('[data-testid="version-row"]:not([data-current])').getByTestId("rollback-button").click()
  await page.getByTestId("confirm-rollback").click()
  await expect(page.getByText("Version 1 is live")).toBeVisible()
  await expect(page.locator('[data-testid="version-row"][data-current]')).toContainText("v1")

  await site.goto(`${origin}/`)
  await expectVersion(site, "one", "rgb(200, 30, 30)")
})

test("a second user gets no access to the site", async ({ page, context, baseURL }) => {
  await signIn(context, baseURL!, stranger.session)
  const origin = siteOrigin(baseURL!, siteId)

  // Their list is empty, with the explanation of how sites are created.
  await page.goto("/")
  await expect(page.getByText("Nothing on the press yet")).toBeVisible()
  await expect(page.getByText("/mcp").first()).toBeVisible()

  // The site host sends them through the handoff, which refuses without
  // issuing a ticket and lands on the app's no-access page.
  const res = await page.goto(`${origin}/about`)
  expect(new URL(page.url()).origin).toBe(baseURL)
  await expect(page).toHaveURL(`/site?id=${siteId}`)
  await expect(page.getByText("You don’t have access to this site")).toBeVisible()
  expect(await res?.text()).not.toContain("Itinerary")

  // No site cookie was set, and the API answers as for a missing site.
  const cookies = await context.cookies(origin)
  expect(cookies.find((c) => c.name === "__Host-site")).toBeUndefined()
  const api = await page.request.get(`/app-api/sites/${siteId}`)
  expect(api.status()).toBe(404)
  const missing = await page.request.get(`/app-api/sites/zzzzzzzzzzzz`)
  expect(await api.text()).toBe(await missing.text())
})
