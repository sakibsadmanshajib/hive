import { defineConfig, devices } from "@playwright/test";

// ponytail: without creds, owui.setup.ts skips and never writes storageState.
// Match zero spec files for the dependent projects in that case so the run
// exits clean (0 failed) instead of every spec ENOENT-ing on the missing
// storageState file. SUPABASE_OAUTH_CLIENT_ID/SECRET gate the OAuth-backed
// "Continue with Hive" journey the same way OWUI_E2E_EMAIL/PASSWORD gate the
// seeded test user -- both must mirror owui.setup.ts's own skip condition.
const hasCreds = Boolean(
  process.env.OWUI_E2E_EMAIL &&
    process.env.OWUI_E2E_PASSWORD &&
    process.env.SUPABASE_OAUTH_CLIENT_ID &&
    process.env.SUPABASE_OAUTH_CLIENT_SECRET,
);

export default defineConfig({
  testDir: "./",
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  reporter: [
    ["list"],
    ["html", { outputFolder: "../../../playwright-report-owui", open: "never" }],
  ],
  use: {
    baseURL: process.env.OWUI_URL ?? "http://localhost:3002",
    trace: "retain-on-failure",
    video: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    { name: "owui-setup", testMatch: /owui\.setup\.ts$/ },
    {
      name: "owui",
      testMatch: hasCreds ? /\d{2}-.*\.spec\.ts$/ : [],
      dependencies: ["owui-setup"],
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "owui-perf",
      testMatch: hasCreds ? /performance\/.*\.spec\.ts$/ : [],
      dependencies: ["owui-setup"],
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
