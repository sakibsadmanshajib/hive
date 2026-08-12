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
  // A retry-pass must not be able to report success. Two layers, because
  // either one alone has a hole:
  //
  //   failOnFlakyTests is Playwright's own gate and survives everything,
  //   including `--reporter=list` on the command line, which replaces the
  //   configured reporters outright and would otherwise silently disable the
  //   custom reporter below and restore exit 0.
  //
  //   flake-reporter is what says *which* test, with its attempt count, in the
  //   job summary. The built-in flag gives an exit code and nothing to act on;
  //   finding the two retry-passes behind CI run 31361681115 without it meant
  //   unzipping a 5MB artifact by hand.
  failOnFlakyTests: true,
  // The `list` reporter folds retry-passes into its "N passed" tail, so
  // without the reporter below a run that needed two retries is
  // indistinguishable from a clean one.
  reporter: process.env.CI
    ? [
        ["list"],
        ["html", { open: "never" }],
        ["./tests/e2e/support/flake-reporter.ts"],
      ]
    : [["html"], ["./tests/e2e/support/flake-reporter.ts"]],
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:3000",
    trace: "retain-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      testDir: "./tests/e2e",
      // The CI job selects this project rather than naming files (issue
      // #813), so anything left in this directory runs there. tests/e2e/_probe
      // must not: those specs point at deployed staging hosts, and two of
      // their tests carry no credential gate, so against a locally booted
      // stack they fail on DNS rather than on anything the change under test
      // did. They keep their own project below so they stay runnable by hand.
      testIgnore: /_probe\//,
      use: { ...devices["Desktop Chrome"] },
    },
    {
      // Manual staging probes. Run with `--project=probe` against a deployed
      // environment, with the HIVE_QA_* identities set. No workflow invokes
      // this, which is why its two spec files are carried as justified debt in
      // tests/dark-spec-allowlist.json rather than silently ignored.
      name: "probe",
      testDir: "./tests/e2e/_probe",
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
      // Everything in the directory except the two that drive Open WebUI,
      // rather than an enumerated 03 through 07. An enumerated list is how a
      // spec goes dark by being added: 08 would have matched nothing and run
      // nowhere, with no error. Adding a spec here now needs a deliberate
      // testIgnore edit to keep it out.
      testMatch: /\/phase-19\/[^/]+\.spec\.ts$/,
      testIgnore: /\/phase-19\/0[12]-[^/]+\.spec\.ts$/,
      use: {
        ...devices["Desktop Chrome"],
        // No browser is driven here, and a trace records request headers
        // verbatim, so retaining one would put a live Supabase bearer token
        // into an artifact that anyone can download from this public
        // repository. `::add-mask::` redacts log text only, never files.
        trace: "off",
        video: "off",
      },
    },
  ],
});
