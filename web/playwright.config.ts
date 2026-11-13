import { defineConfig, devices } from "@playwright/test"

// Runs against an already-running handbill server (the real Go binary with
// the embedded export), never `next dev`: the point is to test how Go serves
// the export. BASE_URL is the app origin; SITES_DOMAIN the sites parent domain.
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: process.env.BASE_URL ?? "http://dash.localhost:8080",
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
})
