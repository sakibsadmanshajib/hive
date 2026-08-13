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
    // The CI artifact excludes traces (*.zip), videos (*.webm) and index.html,
    // because each of those carries request headers, cookies or URLs with a
    // credential in the query string or the fragment, wrapped so that no text
    // linter can inspect them. Without this line the exclusions would leave
    // that artifact empty, since screenshot defaults to "off" and the report
    // directory holds nothing else. A screenshot is the viewport only: no URL
    // bar, no headers, no cookies, and it is inspectable by eye.
    screenshot: "only-on-failure",
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
  ],
});
