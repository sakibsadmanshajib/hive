import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  // E2E specs share a single Supabase fixture state (reset in beforeEach via
  // the e2e-fixtures edge function). Running multiple workers concurrently
  // races on that reset and flaps sessions mid-test, so we serialize.
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
      testMatch: /^[^/]+\.spec\.ts$/,
      use: { ...devices["Desktop Chrome"] },
      dependencies: ["phase-19-setup"],
    },
  ],
});
