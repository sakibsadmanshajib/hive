import { execFile } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import type { BrowserContext } from "@playwright/test";

const execFileAsync = promisify(execFile);

// Spec-side entry to live-auth.mjs, which is the single implementation (see
// its header for how a session is minted and why a password is never touched).
//
// Shells out rather than importing it, for the same two reasons as
// reset-profile.ts: Playwright's transform compiles spec imports to CommonJS,
// and importing a .mjs from a spec makes Node evaluate that CJS output in ES
// module scope ("exports is not defined"); and a child process keeps the
// service-role key out of the Playwright worker's own module graph, so it can
// never reach a page, a trace, or a video.

export interface LiveSessionOptions {
  /** An existing account. Its password is neither needed nor modified. */
  email: string;
  /** Any URL on the app origin the session cookies are for. */
  targetUrl: string;
}

/**
 * Mints a session and writes a Playwright storage state file for it.
 *
 * Awaited rather than synchronous. `execFileSync` freezes the worker's event
 * loop, and Playwright's timeout is an ordinary timer that cannot fire while
 * the loop is blocked, so every timeout in the calling file silently stops
 * working for the duration. `reauthenticate` below is explicitly designed to
 * be called mid-test, which is exactly when that matters: a test could then
 * overrun its deadline and still be reported as passed, which is the defect
 * removed from the fixture reseed in this same change.
 */
export async function writeStorageState(
  { email, targetUrl }: LiveSessionOptions,
  statePath: string,
): Promise<void> {
  try {
    await execFileAsync(
      "node",
      ["tests/e2e/support/live-auth.mjs", email, targetUrl, statePath],
      { cwd: process.cwd(), env: { ...process.env, NODE_OPTIONS: "" } },
    );
  } catch (err: unknown) {
    const e = err as { stdout?: string; stderr?: string };
    // live-auth.mjs redacts its own output, so relaying it is safe.
    throw new Error(`[live-auth] mint failed\n${e.stdout ?? ""}${e.stderr ?? ""}`);
  }
}

/**
 * Signs an open browser context in with a freshly minted session.
 *
 * Also the re-authentication path for a long run: issue #782 kills a session
 * roughly 55 minutes after sign-in (fix in PR #787, unmerged), and a run that
 * cannot renew reports working controls as broken ones.
 */
export async function reauthenticate(
  context: BrowserContext,
  options: LiveSessionOptions,
): Promise<void> {
  const dir = mkdtempSync(join(tmpdir(), "hive-live-auth-"));
  const statePath = join(dir, "state.json");
  try {
    await writeStorageState(options, statePath);
    const state: { cookies: Parameters<BrowserContext["addCookies"]>[0] } = JSON.parse(
      readFileSync(statePath, "utf8"),
    );
    await context.addCookies(state.cookies);
  } finally {
    // The file holds live session cookies. It never outlives the call.
    rmSync(dir, { recursive: true, force: true });
  }
}
