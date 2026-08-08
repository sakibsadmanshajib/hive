import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  // E2E specs share a single Supabase fixture state (reset in beforeEach via
  // tests/e2e/support/e2e-auth-fixtures.mjs). Running multiple workers
  // concurrently races on that reset and flaps sessions mid-test, so we
  // serialize.
  workers: 1,
  reporter: process.env.CI
    ? [["list"], ["html", { open: "never" }]]
    : "html",
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:3000",
    trace: "retain-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      testDir: "./tests/e2e",
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "phase-19-setup",
      testDir: "./e2e/phase-19",
      testMatch: /auth\.setup\.ts$/,
    },
    {
      name: "phase-19",
      testDir: "./e2e/phase-19",
      testMatch: /\/phase-19\/[^/]+\.spec\.ts$/, // full path match, direct children only; owui runs separately
      use: { ...devices["Desktop Chrome"] },
      dependencies: ["phase-19-setup"],
    },
    {
      // The API-only subset of the phase-19 specs (issue #659). Specs 03
      // through 07 assert an HTTP contract against control-plane or edge-api
      // and need no browser session, so they do not depend on
      // phase-19-setup, which signs in through Open WebUI. That dependency
      // is a large part of why the whole suite stayed dark: the per-push
      // job boots control-plane, edge-api and Next.js but not Open WebUI,
      // so nothing that needs an Open WebUI session can run there. Specs 01
      // and 02 genuinely do drive that UI and stay out of this project.
      name: "phase-19-api",
      testDir: "./e2e/phase-19",
      testMatch: /\/phase-19\/0[34567]-[^/]+\.spec\.ts$/,
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
