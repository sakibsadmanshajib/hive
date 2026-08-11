import { defineConfig, devices } from "@playwright/test";

// The interaction coverage gate is origin agnostic on purpose: CI points it
// at the composed stack, a live run points it at the deployed console.
const INTERACTION_BASE_URL =
  process.env.INTERACTION_BASE_URL ??
  process.env.PLAYWRIGHT_BASE_URL ??
  "http://localhost:3000";

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
      // this one yet, so its remaining spec is carried as known debt.
      //
      // The agent-workspace probe is deliberately NOT in here any more: it has
      // its own project below so a workflow can select it by name rather than
      // by file path (issue #813), without dragging console-hive's staging
      // flows into the same job.
      name: "probe",
      testDir: "./tests/e2e/_probe",
      testIgnore: /agent-workspace-flows\.spec\.ts$/,
      use: { ...devices["Desktop Chrome"] },
    },
    // Interaction coverage gate (see tests/interaction/README.md). Origin is
    // configurable so the same run targets the composed stack in CI and the
    // deployed box for a live run.
    //
    // Both projects run with retries: 0, against the repository default of two.
    // A retry here is worse than useless: the sweep is one test that walks the
    // whole console for tens of minutes, so a retry re-walks all of it, and a
    // control that only works on the second attempt is exactly the defect this
    // gate exists to report rather than to absorb.
    {
      name: "interaction-setup",
      testDir: "./tests/interaction",
      testMatch: /auth\.setup\.ts$/,
      retries: 0,
      use: {
        ...devices["Desktop Chrome"],
        baseURL: INTERACTION_BASE_URL,
        // No trace and no video for this suite, for two independent reasons.
        //
        // Leak: the sweep types into password fields, and a trace carries the
        // Authorization header of every XHR the console made. Issue #554 is
        // the same problem in the HTML report's ARIA snapshots, which is why
        // the CI job already refuses to upload that. An artifact nobody may
        // upload is not worth recording.
        //
        // Cost: a failing run of a whole-application sweep spent over ten
        // minutes after the verdict finalizing artifacts, on a job with a 60
        // minute cap. The evidence this gate produces is its own JSON ledger,
        // which is written after every route and uploaded unconditionally.
        trace: "off",
        video: "off",
      },
      // A live origin over the public internet signs in slower than a local
      // stack; the default 30s cut the wait off mid-redirect.
      timeout: 120 * 1000,
    },
    {
      name: "interaction",
      testDir: "./tests/interaction",
      testMatch: /interaction-coverage\.spec\.ts$/,
      retries: 0,
      use: {
        ...devices["Desktop Chrome"],
        baseURL: INTERACTION_BASE_URL,
        // No trace and no video for this suite, for two independent reasons.
        //
        // Leak: the sweep types into password fields, and a trace carries the
        // Authorization header of every XHR the console made. Issue #554 is
        // the same problem in the HTML report's ARIA snapshots, which is why
        // the CI job already refuses to upload that. An artifact nobody may
        // upload is not worth recording.
        //
        // Cost: a failing run of a whole-application sweep spent over ten
        // minutes after the verdict finalizing artifacts, on a job with a 60
        // minute cap. The evidence this gate produces is its own JSON ledger,
        // which is written after every route and uploaded unconditionally.
        trace: "off",
        video: "off",
      },
      dependencies: ["interaction-setup"],
      timeout: 3 * 60 * 60 * 1000,
    },
    {
      // Interaction-coverage probe for the agent workspace (Cowork), run
      // against the deployed chat host by
      // .github/workflows/deploy-demo-box.yml. It needs SUPABASE_URL,
      // SUPABASE_SERVICE_ROLE_KEY and SUPABASE_ANON_KEY to mint a session
      // (tests/e2e/support/live-auth.mjs) and fails hard without them, because
      // a skip here silently drops sixteen controls out of the ratio.
      name: "agent-workspace",
      testDir: "./tests/e2e/_probe",
      testMatch: /agent-workspace-flows\.spec\.ts$/,
      use: {
        ...devices["Desktop Chrome"],
        /*
         * No trace and no video for this project, overriding the
         * retain-on-failure defaults above. This is the one project that
         * drives a browser with a REAL session on a deployed host, and a
         * Playwright trace stores request headers and cookies verbatim: the
         * Authorization bearer on every agent-task call, and the
         * sb-*-auth-token cookie, which carries the refresh token for a shared
         * account. This repository is public and its artifacts are retained
         * for 90 days, so a single failed run would publish a live credential.
         * live-auth.mjs redacts its own output; it cannot reach inside a
         * browser trace.
         *
         * The trade is deliberate: a probe of a deployed surface is
         * reproducible by re-running it against that surface, which is not
         * true of a CI job whose stack is gone. Debug it locally with
         * `--trace on` against a throwaway identity, never in CI.
         */
        trace: "off",
        video: "off",
      },
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
