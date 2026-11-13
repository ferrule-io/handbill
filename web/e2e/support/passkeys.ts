import { expect, type CDPSession, type Page } from "@playwright/test"

/**
 * A CDP virtual authenticator: a platform authenticator with resident keys
 * and user verification that answers every prompt automatically, like a
 * device unlocked with Touch ID.
 */
export class VirtualAuthenticator {
  private constructor(
    private cdp: CDPSession,
    readonly id: string
  ) {}

  static async attach(page: Page): Promise<VirtualAuthenticator> {
    const cdp = await page.context().newCDPSession(page)
    await cdp.send("WebAuthn.enable")
    const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
      options: {
        protocol: "ctap2",
        transport: "internal",
        hasResidentKey: true,
        hasUserVerification: true,
        isUserVerified: true,
        automaticPresenceSimulation: true,
      },
    })
    return new VirtualAuthenticator(cdp, authenticatorId)
  }

  async credentialCount(): Promise<number> {
    const { credentials } = await this.cdp.send("WebAuthn.getCredentials", { authenticatorId: this.id })
    return credentials.length
  }

  /** Removes the authenticator and its passkeys (a lost device). */
  async remove() {
    await this.cdp.send("WebAuthn.removeVirtualAuthenticator", { authenticatorId: this.id })
  }
}

export function uniqueEmail(prefix = "e2e"): string {
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}@example.com`
}

/**
 * Signs up through the UI with a passkey from a virtual authenticator,
 * saves the recovery codes, and lands signed in. Returns the codes.
 */
export async function signUp(page: Page, email: string): Promise<string[]> {
  await page.goto("/signup")
  await page.getByLabel("Email").fill(email)
  await page.getByRole("button", { name: "Create with passkey" }).click()
  await expect(page).toHaveURL(/\/signup\/codes/)
  const codes = page.getByTestId("recovery-code")
  await expect(codes).toHaveCount(10)
  const values = await codes.allTextContents()
  await page.getByLabel(/saved my recovery codes/).check()
  await page.getByRole("button", { name: "Continue to handbill" }).click()
  await expect(page.getByTestId("account-menu")).toBeVisible()
  return values
}

export async function signOut(page: Page) {
  await page.getByTestId("account-menu").click()
  await page.getByRole("menuitem", { name: "Sign out" }).click()
  await expect(page).toHaveURL(/\/login/)
}
