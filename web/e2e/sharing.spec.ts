import { expect, test, type Page } from "@playwright/test"

import { VirtualAuthenticator } from "./support/passkeys"
import { seed, signIn, siteOrigin, uniqueEmail } from "./support/seed"

// Sharing end to end (docs/roadmap.md step 6): invites that bind to the
// account that accepts them, manager limits in the UI and the API, removal
// taking effect on the next request, and the public link with rotation.

const PAGE = (title: string) =>
  `<!doctype html><html><head><meta charset="utf-8"><title>${title}</title></head><body><h1>${title}</h1></body></html>`

type Seeded = { siteId: string; sessions: Record<string, string> }

function seedSite(title: string, members: { label: string; role: "manager" | "viewer" }[] = []): Seeded & {
  owner: string
  emails: Record<string, string>
} {
  const owner = uniqueEmail("owner")
  const emails: Record<string, string> = { owner }
  for (const m of members) emails[m.label] = uniqueEmail(m.label)
  const out = seed({
    users: Object.values(emails).map((email) => ({ email })),
    sites: [
      {
        owner,
        title,
        members: members.map((m) => ({ email: emails[m.label], role: m.role })),
        versions: [{ files: [{ path: "index.html", text: PAGE(title) }] }],
      },
    ],
  })
  const sessions: Record<string, string> = {}
  for (const [label, email] of Object.entries(emails)) sessions[label] = out.users[email].sessionToken
  return { siteId: out.sites[0].id, sessions, owner, emails }
}

/** POSTs JSON from the page like the UI does (session cookie, Origin, CSRF). */
async function apiPost(page: Page, path: string, body: unknown): Promise<number> {
  return page.evaluate(
    async ({ path, body }) => {
      const me = await (await fetch("/app-api/me")).json()
      const res = await fetch(path, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": me.csrfToken },
        body: JSON.stringify(body),
      })
      return res.status
    },
    { path, body }
  )
}

test.describe.configure({ timeout: 180_000 })

test("an invite link signs a new person up and lands them on the site", async ({ page, context, browser, baseURL }) => {
  const s = seedSite("Car shortlist")
  await context.grantPermissions(["clipboard-read", "clipboard-write"])
  await signIn(context, baseURL!, s.sessions.owner)
  await page.goto(`/site?id=${s.siteId}`)

  const invitee = uniqueEmail("invitee")
  const members = page.getByRole("form", { name: "Invite someone" })
  await members.getByLabel("Email address").fill(invitee)
  await page.getByTestId("send-invite").click()
  const panel = page.getByTestId("invite-link-panel")
  await expect(panel).toContainText(`Invite link for ${invitee}`)
  await panel.getByTestId("copy-link").click()
  await expect(panel.getByTestId("copy-link")).toHaveText(/Copied/)
  const link = await page.evaluate(() => navigator.clipboard.readText())
  expect(link).toBe(await panel.getByTestId("issued-link").inputValue())
  expect(link).toMatch(new RegExp(`^${baseURL}/invite#token=[A-Za-z0-9_-]{43}$`))
  await expect(page.locator(`[data-testid="invite-row"][data-email="${invitee}"]`)).toBeVisible()

  // A stranger's browser: no session, a passkey authenticator.
  const fresh = await browser.newContext()
  const guest = await fresh.newPage()
  await VirtualAuthenticator.attach(guest)
  await guest.goto(link)
  await expect(guest.getByTestId("invite-site-title")).toHaveText("Car shortlist")
  await expect(guest.getByTestId("invite-summary")).toContainText(s.owner)
  await expect(guest.getByTestId("invite-summary")).toContainText("as a viewer")
  await guest.getByTestId("invite-signup").click()
  await expect(guest).toHaveURL(/\/signup\?return=%2Finvite$/)
  await guest.getByLabel("Email").fill(invitee)
  await guest.getByRole("button", { name: "Create with passkey" }).click()
  await expect(guest).toHaveURL(/\/signup\/codes/)
  await guest.getByLabel(/saved my recovery codes/).check()
  await guest.getByRole("button", { name: "Continue to Handbill" }).click()

  // Back on /invite, the invite is accepted automatically.
  await expect(guest).toHaveURL(`/site?id=${s.siteId}&welcome=1`)
  await expect(guest.getByTestId("welcome-banner")).toContainText("You’re in as a viewer")
  await expect(guest.getByTestId("site-title")).toHaveText("Car shortlist")
  expect(await guest.evaluate(() => sessionStorage.getItem("handbill.invite.token"))).toBeNull()

  const [site] = await Promise.all([fresh.waitForEvent("page"), guest.getByTestId("welcome-open-site").click()])
  await expect(site).toHaveURL(`${siteOrigin(baseURL!, s.siteId)}/`)
  await expect(site.getByRole("heading", { level: 1 })).toHaveText("Car shortlist")

  // The link is spent: opening it again just leads back to the site.
  await guest.goto(link)
  await expect(guest.getByTestId("go-to-site")).toBeVisible()

  // The owner sees the new member and no pending invite.
  await page.reload()
  await expect(page.locator(`[data-testid="member-row"][data-email="${invitee}"]`)).toContainText(/Viewer/)
  await expect(page.getByTestId("invite-list")).toHaveCount(0)
  await fresh.close()
})

test("a manager publishes and invites viewers but cannot add managers", async ({ page, context, baseURL }) => {
  const s = seedSite("Daily agenda", [{ label: "manager", role: "manager" }])
  await signIn(context, baseURL!, s.sessions.manager)
  await page.goto(`/site?id=${s.siteId}`)

  // Publish-adjacent actions are there: details, versions.
  await expect(page.getByTestId("edit-details")).toBeVisible()
  await expect(page.getByTestId("version-row")).toHaveCount(1)
  // Sharing is limited to viewers: no manager role, no public-link controls, no transfer.
  await expect(page.getByRole("form", { name: "Invite someone" })).toContainText("Only the owner can add managers")
  await expect(page.getByTestId("invite-role-manager")).toHaveCount(0)
  await expect(page.getByTestId("public-link-state")).toHaveText(/Off/)
  await expect(page.getByTestId("enable-public-link")).toHaveCount(0)
  await expect(page.getByTestId("transfer")).toHaveCount(0)
  // The owner's and their own rows have no controls.
  await expect(page.getByTestId("member-menu")).toHaveCount(0)

  const viewer = uniqueEmail("viewer")
  await page.getByLabel("Email address").fill(viewer)
  await page.getByTestId("send-invite").click()
  await expect(page.getByTestId("invite-link-panel")).toContainText(viewer)

  // The API refuses what the UI hides.
  expect(await apiPost(page, `/app-api/sites/${s.siteId}/members`, { email: uniqueEmail("m"), role: "manager", notify: false })).toBe(403)
  expect(await apiPost(page, `/app-api/sites/${s.siteId}/public-link`, { op: "enable" })).toBe(403)
  expect(await apiPost(page, `/app-api/sites/${s.siteId}/transfer`, { email: uniqueEmail("t"), notify: false })).toBe(403)
  expect(await apiPost(page, `/app-api/sites/${s.siteId}/members`, { email: uniqueEmail("v"), role: "viewer", notify: false })).toBe(200)
})

test("a removed viewer loses the site on their next request", async ({ page, context, browser, baseURL }) => {
  const s = seedSite("Vacation options", [{ label: "viewer", role: "viewer" }])
  const origin = siteOrigin(baseURL!, s.siteId)

  const viewerContext = await browser.newContext()
  await signIn(viewerContext, baseURL!, s.sessions.viewer)
  const viewer = await viewerContext.newPage()
  await viewer.goto(`${origin}/`)
  await expect(viewer).toHaveURL(`${origin}/`)
  await expect(viewer.getByRole("heading", { level: 1 })).toHaveText("Vacation options")

  await signIn(context, baseURL!, s.sessions.owner)
  await page.goto(`/site?id=${s.siteId}`)
  const row = page.locator(`[data-testid="member-row"][data-email="${s.emails.viewer}"]`)
  await row.getByTestId("member-menu").click()
  await page.getByRole("menuitem", { name: "Remove access" }).click()
  await page.getByTestId("confirm-remove").click()
  await expect(row).toHaveCount(0)

  // Same tab, same site cookie: refused, and the handoff lands on no-access.
  await viewer.reload()
  await expect(viewer).toHaveURL(`/site?id=${s.siteId}`)
  await expect(viewer.getByText("You don’t have access to this site")).toBeVisible()
  await viewerContext.close()
})

test("the public link works signed out, and rotating it cuts off the old link and cookie", async ({
  page,
  context,
  browser,
  baseURL,
}) => {
  const s = seedSite("Shared itinerary")
  const origin = siteOrigin(baseURL!, s.siteId)
  await signIn(context, baseURL!, s.sessions.owner)
  await page.goto(`/site?id=${s.siteId}`)

  await page.getByTestId("enable-public-link").click()
  const panel = page.getByTestId("public-link-panel")
  await expect(panel).toContainText("Shown only now")
  const link = await panel.getByTestId("issued-link").inputValue()
  expect(link).toMatch(new RegExp(`^${origin}/_auth/share/[A-Za-z0-9_-]{43}$`))
  await expect(page.getByTestId("public-link-state")).toHaveText(/On/)

  const anonContext = await browser.newContext()
  const anon = await anonContext.newPage()
  await anon.goto(link)
  await expect(anon).toHaveURL(`${origin}/`)
  await expect(anon.getByRole("heading", { level: 1 })).toHaveText("Shared itinerary")

  await page.getByTestId("rotate-public-link").click()
  await page.getByTestId("confirm-action").click()
  await expect(page.getByTestId("public-link-panel")).toContainText("previous link no longer works")
  const rotated = await page.getByTestId("issued-link").inputValue()
  expect(rotated).not.toBe(link)

  // The old cookie is refused on the next request: back to sign-in.
  await anon.reload()
  await expect(anon).toHaveURL(/\/login\?return=/)
  // The old link is dead; the new one works.
  const old = await anon.goto(link)
  expect(old?.status()).toBe(404)
  await expect(anon.getByRole("heading", { level: 1 })).toHaveText("This link isn’t active")
  await anon.goto(rotated)
  await expect(anon).toHaveURL(`${origin}/`)
  await expect(anon.getByRole("heading", { level: 1 })).toHaveText("Shared itinerary")

  // Turning it off refuses the current cookie too.
  await page.getByTestId("disable-public-link").click()
  await page.getByTestId("confirm-action").click()
  await expect(page.getByTestId("public-link-state")).toHaveText(/Off/)
  await anon.reload()
  await expect(anon).toHaveURL(/\/login\?return=/)
  await anonContext.close()
})
