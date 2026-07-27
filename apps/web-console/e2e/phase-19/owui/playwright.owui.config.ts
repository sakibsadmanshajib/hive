import { defineConfig, devices } from "@playwright/test";

// ponytail: without creds, owui.setup.ts skips and never writes storageState.
// Match zero spec files for the dependent projects in that case so the run
// exits clean (0 failed) instead of every spec ENOENT-ing on the missing
// storageState file. SUPABASE_OAUTH_CLIENT_ID/SECRET gate the OAuth-backed
// "Continue with Hive" journey the same way OWUI_E2E_EMAIL/PASSWORD gate the
// seeded test user -- both must mirror owui.setup.ts's own skip condition.
const hasUserCreds = Boolean(
  process.env.OWUI_E2E_EMAIL && process.env.OWUI_E2E_PASSWORD,
);
const hasCreds = Boolean(
  hasUserCreds &&
    process.env.SUPABASE_OAUTH_CLIENT_ID &&
    process.env.SUPABASE_OAUTH_CLIENT_SECRET,
);

export default defineConfig({
  testDir: "./",
  // 60s (not the default 30s): the per-assertion timeouts in the chat
  // specs run up to 45s to absorb real free-tier OpenRouter/Groq latency
  // now that auth+routing succeed end-to-end (run 28691819361) -- the
  // test-level timeout governs the whole test function and would
  // otherwise cut those assertions off before they get their own budget.
  timeout: 60_000,
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
    {
      // Logs in from scratch against a deployed OWUI_URL, so it takes no
      // storageState and depends on no setup project. The spec skips itself
      // when OWUI_URL is a loopback address, which leaves the nightly
      // unaffected. Gated on the user credentials only, not on
      // SUPABASE_OAUTH_CLIENT_*: for a deployed target the OAuth client is
      // configured on that deployment, not in whatever environment runs this.
      name: "owui-deployed-login",
      testMatch: hasUserCreds ? /deployed-login\.spec\.ts$/ : [],
      // Generous on purpose. The journey crosses two origins over the public
      // internet, carries the same fill-and-submit retry budget as
      // owui.setup.ts, and ends on a full SPA load. A test timeout smaller
      // than the sum of its assertion timeouts truncates an assertion before
      // it gets its own budget, which is a confusing way to fail.
      timeout: 300_000,
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
