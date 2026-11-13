import { expect, test } from "@playwright/test"

import { signOut, signUp, uniqueEmail, VirtualAuthenticator } from "./support/passkeys"

// Accounts acceptance check (docs/roadmap.md step 3) against the real Go
// binary, with a CDP virtual authenticator standing in for Touch ID.

test.beforeEach(({ page }) => {
  page.on("pageerror", (err) => {
    throw err
  })
})

test("passkey sign-up, usernameless sign-in, and recovery with a code", async ({ page }) => {
  const email = uniqueEmail("viewer")
  const device = await VirtualAuthenticator.attach(page)

  // Sign up: one screen, then the recovery codes.
  const codes = await signUp(page, email)
  expect(codes).toHaveLength(10)
  for (const code of codes) expect(code).toMatch(/^[A-Z2-7]{4}(-[A-Z2-7]{4}){3}$/)
  expect(await device.credentialCount()).toBe(1)
  await page.getByTestId("account-menu").click()
  await expect(page.getByTestId("account-email")).toHaveText(email)
  await page.keyboard.press("Escape")
  await expect(page.getByRole("menu")).toBeHidden()

  // Sign out, then back in without typing anything.
  await signOut(page)
  await page.getByRole("button", { name: "Sign in with passkey" }).click()
  await expect(page.getByTestId("account-menu")).toBeVisible()

  // Settings lists the passkey.
  await page.getByRole("link", { name: "Security" }).click()
  await expect(page).toHaveURL("/settings/security")
  await expect(page.getByTestId("passkey-name")).toHaveCount(1)
  await expect(page.getByTestId("recovery-remaining")).toHaveText("10 of 10 left")

  // Lose the device: recover with a code and register a new passkey.
  await signOut(page)
  await device.remove()
  const newDevice = await VirtualAuthenticator.attach(page)
  await page.getByRole("link", { name: /Use a recovery code/ }).click()
  await expect(page).toHaveURL(/\/login\/recover/)
  await page.getByLabel("Email").fill(email)
  await page.getByLabel("Recovery code").fill(codes[0].toLowerCase())
  await page.getByRole("button", { name: "Continue" }).click()
  await expect(page.getByRole("heading", { name: "Now, a new passkey" })).toBeVisible()

  // The recovery session can't reach the app until the passkey exists.
  const blocked = await page.evaluate(async () => (await fetch("/app-api/auth/security")).status)
  expect(blocked).toBe(401)

  await page.getByRole("button", { name: "Create a new passkey" }).click()
  await expect(page).toHaveURL("/settings/security")
  await expect(page.getByTestId("passkey-name")).toHaveCount(2)
  await expect(page.getByTestId("recovery-remaining")).toHaveText("9 of 10 left")
  expect(await newDevice.credentialCount()).toBe(1)

  // A spent code doesn't work twice; the new passkey signs in.
  await signOut(page)
  await page.goto("/login/recover")
  await page.getByLabel("Email").fill(email)
  await page.getByLabel("Recovery code").fill(codes[0])
  await page.getByRole("button", { name: "Continue" }).click()
  await expect(page.getByText(/recovery code don't match/)).toBeVisible()
  await page.goto("/login")
  await page.getByRole("button", { name: "Sign in with passkey" }).click()
  await expect(page.getByTestId("account-menu")).toBeVisible()
})

test("signed-out visitors are sent to sign in and come back", async ({ page }) => {
  await VirtualAuthenticator.attach(page)
  const res = await page.goto("/settings/security")
  expect(res?.status()).toBe(200)
  await expect(page).toHaveURL("/login?return=%2Fsettings%2Fsecurity")
  await page.getByRole("link", { name: "Create an account" }).click()
  await expect(page).toHaveURL("/signup?return=%2Fsettings%2Fsecurity")
  await page.getByLabel("Email").fill(uniqueEmail("return"))
  await page.getByRole("button", { name: "Create with passkey" }).click()
  await expect(page.getByTestId("recovery-code")).toHaveCount(10)
  await page.getByLabel(/saved my recovery codes/).check()
  await page.getByRole("button", { name: "Continue to handbill" }).click()
  await expect(page).toHaveURL("/settings/security")
  await expect(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible()
})

test("an unsafe return path is ignored", async ({ page }) => {
  await VirtualAuthenticator.attach(page)
  await signUp(page, uniqueEmail("unsafe"))
  await signOut(page)
  await page.goto("/login?return=//evil.example/")
  await page.getByRole("button", { name: "Sign in with passkey" }).click()
  await expect(page.getByTestId("account-menu")).toBeVisible()
  await expect(page).toHaveURL("/")
})
