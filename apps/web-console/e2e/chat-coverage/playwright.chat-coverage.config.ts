import path from "node:path";

import { defineConfig, devices } from "@playwright/test";

// The sweep is one long serial journey against a live deployment, so it gets
// its own config rather than a project inside playwright.owui.config.ts: the
// timeouts here are an order of magnitude larger and the auth path differs.
const CHAT = process.env.CHAT_URL ?? process.env.OWUI_URL ?? "";
const hasTarget = Boolean(CHAT);
const hasCreds = Boolean(
  (process.env.OWUI_E2E_EMAIL && process.env.OWUI_E2E_PASSWORD) ||
    (process.env.SUPABASE_SERVICE_ROLE_KEY &&
      process.env.SUPABASE_ANON_KEY &&
      process.env.SUPABASE_URL &&
      process.env.CONSOLE_URL &&
      process.env.OWUI_E2E_EMAIL),
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
