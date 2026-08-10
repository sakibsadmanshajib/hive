import path from "node:path";

import { defineConfig, devices } from "@playwright/test";

// The sweep is one long serial journey against a live deployment, so it gets
// its own config rather than a project inside playwright.owui.config.ts: the
// timeouts here are an order of magnitude larger and the auth path differs.
const CHAT = process.env.CHAT_URL ?? process.env.OWUI_URL ?? "";
const hasTarget = Boolean(CHAT);
// live-auth.mjs mints the session, so what is needed is an account to mint for
// and the keys it uses. No password is involved at any point.
const hasCreds = Boolean(
  (process.env.OWUI_E2E_EMAIL || process.env.HIVE_QA_AGENT_EMAIL) &&
    process.env.SUPABASE_URL &&
    process.env.SUPABASE_SERVICE_ROLE_KEY &&
    process.env.SUPABASE_ANON_KEY,
);
const runnable = hasTarget && hasCreds;

export default defineConfig({
  testDir: "./",
  timeout: 45 * 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [
    ["list"],
    ["json", { outputFile: "../../chat-coverage-report/playwright.json" }],
    ["html", { outputFolder: "../../playwright-report-chat-coverage", open: "never" }],
  ],
  use: {
    baseURL: CHAT || "http://localhost:3002",
    // Playwright's default for both is 0, meaning "borrow the whole test
    // timeout". With a 45 minute test that turns one stalled navigation into a
    // dead run with no diagnosis, which is exactly how this suite lost its
    // first two attempts at a number.
    navigationTimeout: 60_000,
    actionTimeout: 20_000,
    trace: "retain-on-failure",
    video: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    { name: "setup", testMatch: runnable ? /auth\.setup\.ts$/ : [] },
    {
      name: "coverage",
      testMatch: runnable ? /chat-coverage\.spec\.ts$/ : [],
      dependencies: ["setup"],
      use: {
        ...devices["Desktop Chrome"],
        viewport: { width: 1440, height: 950 },
        storageState: path.join(__dirname, ".auth", "state.json"),
      },
    },
  ],
});
