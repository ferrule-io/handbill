import AxeBuilder from "@axe-core/playwright"
import { expect, test, type Locator, type Page } from "@playwright/test"

import { pkce, seedClient } from "./support/oauth"
import { seed, signIn, uniqueEmail } from "./support/seed"

// Accessibility (docs/roadmap.md step 7): axe on every main screen, in light
// and dark, failing on serious or critical WCAG 2.2 A/AA violations.

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22a", "wcag22aa"]

let owner: string
let stranger: string
let siteId: string
let inviteToken: string
let clientId: string

test.beforeAll(async ({ browser, baseURL }) => {
  test.setTimeout(240_000)
  const ownerEmail = uniqueEmail("a11y-owner")
  const strangerEmail = uniqueEmail("a11y-stranger")
  const out = seed({
    users: [{ email: ownerEmail }, { email: strangerEmail }],
    sites: [
      {
        owner: ownerEmail,
        title: "Lisbon in October",
        description: "Three itineraries for the long weekend.",
        versions: [{ files: [{ path: "index.html", text: "<!doctype html><title>Lisbon</title><h1>Lisbon</h1>" }] }],
      },
    ],
  })
  owner = out.users[ownerEmail].sessionToken
  stranger = out.users[strangerEmail].sessionToken
  siteId = out.sites[0].id
  clientId = seedClient().clientId

  const context = await browser.newContext({ baseURL })
  await signIn(context, baseURL!, owner)
  const page = await context.newPage()
  await page.goto("/")
  const link = await page.evaluate(async (id) => {
    const me = await (await fetch("/app-api/me")).json()
    const res = await fetch(`/app-api/sites/${id}/members`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": me.csrfToken },
      body: JSON.stringify({ email: "guest@a11y.test", role: "viewer", notify: false }),
    })
    return (await res.json()).link as string
  }, siteId)
  inviteToken = new URL(link).hash.replace("#token=", "")
  await context.close()
})

type Screen = {
  name: string
  as?: () => string
  url: () => string | Promise<string>
  ready: (page: Page) => Locator
}

const screens: Screen[] = [
  { name: "sign-up", url: () => "/signup", ready: (p) => p.getByRole("heading", { level: 1 }) },
  { name: "sign-in", url: () => "/login", ready: (p) => p.getByRole("heading", { level: 1 }) },
  { name: "recovery", url: () => "/login/recover", ready: (p) => p.getByRole("heading", { level: 1 }) },
  { name: "invite", url: () => `/invite#token=${inviteToken}`, ready: (p) => p.getByTestId("invite-ready") },
  { name: "invite, unknown link", url: () => "/invite#token=nope", ready: (p) => p.getByTestId("invite-unusable") },
  { name: "consent error", url: () => "/consent?error=invalid_request", ready: (p) => p.getByTestId("consent-error") },
  { name: "not found", url: () => "/no-such-page", ready: (p) => p.getByTestId("not-found") },
  { name: "sites", as: () => owner, url: () => "/", ready: (p) => p.getByTestId("site-list") },
  { name: "sites, empty", as: () => stranger, url: () => "/", ready: (p) => p.getByText("Nothing on the press yet") },
  { name: "site", as: () => owner, url: () => `/site?id=${siteId}`, ready: (p) => p.getByTestId("member-list") },
  {
    name: "site, no access",
    as: () => stranger,
    url: () => `/site?id=${siteId}`,
    ready: (p) => p.getByText("You don’t have access to this site"),
  },
  {
    name: "security settings",
    as: () => owner,
    url: () => "/settings/security",
    ready: (p) => p.getByRole("heading", { name: "Connected agents" }),
  },
  {
    name: "consent",
    as: () => owner,
    url: () => {
      const q = new URLSearchParams({
        client_id: clientId,
        redirect_uri: "http://127.0.0.1:9/callback",
        response_type: "code",
        code_challenge: pkce().challenge,
        code_challenge_method: "S256",
        state: "a11y",
      })
      return `/oauth/authorize?${q}`
    },
    ready: (p) => p.getByTestId("consent-card"),
  },
]

for (const scheme of ["light", "dark"] as const) {
  test.describe(`${scheme} mode`, () => {
    test.use({ colorScheme: scheme })

    for (const screen of screens) {
      test(`${screen.name} has no serious accessibility violations`, async ({ page, context, baseURL }) => {
        if (screen.as) await signIn(context, baseURL!, screen.as())
        await page.goto(await screen.url())
        await expect(screen.ready(page)).toBeVisible()
        await expect(page.locator("html")).toHaveClass(scheme === "dark" ? /\bdark\b/ : /^(?!.*\bdark\b)/)
        // Let entrance animations (and the consent screen's Allow delay)
        // finish so colors are final.
        await page.waitForTimeout(800)

        const { violations } = await new AxeBuilder({ page }).withTags(TAGS).analyze()
        const blocking = violations
          .filter((v) => v.impact === "serious" || v.impact === "critical")
          .map((v) => `${v.id} (${v.impact}): ${v.help}\n  ${v.nodes.map((n) => n.target.join(" ")).join("\n  ")}`)
        expect(blocking, blocking.join("\n")).toEqual([])
      })
    }
  })
}
