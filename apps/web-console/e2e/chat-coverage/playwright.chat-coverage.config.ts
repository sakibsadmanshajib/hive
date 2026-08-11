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
// Only the LIVE projects are gated on credentials. break-proof.spec.ts needs
// none: it serves its own fixture from an intercepted origin, and it is the
// check that proves the detector can still tell a working control from a
// broken one. Gating it too meant the one test that watches the gate fail
// could only run on a machine that could already run everything else.
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
    // CREDENTIALS. A Playwright trace records every request with its headers,
    // and a video records the address bar, so a trace or a video of any
    // project here holds this account's live session cookies, and one of the
    // setup project holds the OAuth callback's `code` and `state` as well.
    // tools/lint-no-token-in-proof-captures.mjs cannot see inside either: it
    // skips binaries by design. So both are off unless someone deliberately
    // asks for them while debugging, and the artefact that produces must never
    // be attached to a pull request. Screenshots stay on: they frame the page,
    // not the URL bar, and they are the visual proof the merge rule wants.
    trace: process.env.COV_TRACE === "1" ? "retain-on-failure" : "off",
    video: process.env.COV_TRACE === "1" ? "retain-on-failure" : "off",
    screenshot: "only-on-failure",
  },
  projects: [
    {
      name: "chat-coverage-break-proof",
      testMatch: /break-proof\.spec\.ts$/,
    },
    {
      name: "chat-coverage-setup",
      testMatch: /auth\.setup\.ts$/,
      // Belt and braces on top of the switch above: this is the project that
      // walks the OAuth hop, so it never records a trace, a video or a
      // screenshot, whatever COV_TRACE says.
      use: { trace: "off", video: "off", screenshot: "off" },
    },
    {
      name: "chat-coverage",
      testMatch: runnable ? /chat-coverage\.spec\.ts$/ : [],
      dependencies: ["chat-coverage-setup"],
      use: {
        ...devices["Desktop Chrome"],
        viewport: { width: 1440, height: 950 },
        storageState: path.join(__dirname, ".auth", "state.json"),
      },
    },
  ],
});
