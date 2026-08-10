import { execFile } from "node:child_process";
import { promisify } from "node:util";
import type { TestInfo } from "@playwright/test";

const execFileAsync = promisify(execFile);

// Ceiling for one reseed. Generous on purpose: the point is not to be tight,
// it is to make a wedged seeder die with its own message ("reseed exceeded
// 120000ms") instead of surfacing as a Playwright test timeout attributed to
// whatever the browser happened to be doing.
const RESEED_TIMEOUT_MS = 120_000;

/**
 * Reseed the shared Supabase E2E fixtures, and charge the time it took to the
 * hook rather than to the test.
 *
 * Two defects are being closed here, both measured in CI job 31361681115.
 *
 * 1. The reseed talks to a shared cloud Supabase project, so its wall time is
 *    set by conditions no spec controls: the same call took 2.6s, 19.2s,
 *    19.9s and 30.2s across four attempts in that one job. `beforeEach` shares
 *    the test's timeout, so every spec here was running with a random slice of
 *    its declared 30s budget, and two tests failed with the browser behaving
 *    perfectly: `profile settings stay reachable while unverified` burned the
 *    whole 30s inside the hook and never opened a page at all. Extending the
 *    deadline by the hook's measured wall time gives the product back exactly
 *    the budget the spec author asked for, and no more.
 *
 * 2. The reseed used to run through `execFileSync`, which freezes the worker's
 *    event loop, and Playwright's timeout is an ordinary timer. It cannot fire
 *    while the loop is blocked. `setup saves profile` was therefore reported
 *    as **passed** at 30194ms under a 30000ms timeout: a test that overran its
 *    budget and went green anyway. Awaiting an async child keeps the timer
 *    honest.
 *
 * Note on the ordering guarantee in `e2e-fixture-seed.mjs`: awaiting instead
 * of blocking does not widen the window that comment is about. This hook is
 * awaited to completion before the test body runs, `playwright.config.ts`
 * pins `workers: 1`, and the `context`/`page` fixtures are not built until
 * after every `beforeEach` returns, so there is still no page in existence
 * that could observe a half-applied reseed.
 *
 * Still a child process rather than an import: Playwright compiles spec
 * imports to CommonJS, so importing the `.mjs` seeder from a spec makes Node
 * evaluate that output in ES module scope ("exports is not defined"). The
 * child also keeps the service-role key out of the worker's module graph.
 *
 * @param args extra argv for the fixture CLI, e.g. `["reset-profile", email]`.
 */
export async function reseedFixtures(
  testInfo: TestInfo,
  args: readonly string[] = []
): Promise<void> {
  const productBudgetMs = testInfo.timeout;
  // Hold the deadline open while the hook runs so Playwright cannot fire
  // first and blame the product for the seeder's latency. `timeout: 0` means
  // the run has no deadline at all; leave that alone rather than inventing one.
  if (productBudgetMs > 0) {
    testInfo.setTimeout(productBudgetMs + RESEED_TIMEOUT_MS);
  }
  const startedAt = Date.now();
  try {
    await execFileAsync(
      "node",
      ["tests/e2e/support/e2e-auth-fixtures.mjs", ...args],
      {
        cwd: process.cwd(),
        env: { ...process.env, NODE_OPTIONS: "" },
        timeout: RESEED_TIMEOUT_MS,
      }
    );
  } catch (err: unknown) {
    const e = err as { stdout?: string; stderr?: string };
    process.stderr.write(
      `[e2e-auth-fixtures] reset failed after ${Date.now() - startedAt}ms\n${
        e.stdout ?? ""
      }${e.stderr ?? ""}\n`
    );
    throw err;
  } finally {
    // Settle on the declared budget plus what this hook actually consumed.
    // Composes: a second reseeding hook in the same test reads the already
    // extended value and adds its own elapsed time on top.
    if (productBudgetMs > 0) {
      testInfo.setTimeout(productBudgetMs + (Date.now() - startedAt));
    }
  }
}
