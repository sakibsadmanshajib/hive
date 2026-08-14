import { execFile } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import type { BrowserContext, Page } from "@playwright/test";

import { redactText } from "./redact";

const execFileAsync = promisify(execFile);

// Spec-side entry to live-auth.mjs, which is the single implementation (see
// its header for how a session is minted and why a password is never touched).
//
// Shells out rather than importing it, for the same two reasons as
// reset-profile.ts: Playwright's transform compiles spec imports to CommonJS,
// and importing a .mjs from a spec makes Node evaluate that CJS output in ES
// module scope ("exports is not defined"); and a child process keeps the
// service-role key out of this module's own graph, so no value derived from it
// is in scope where a spec could pass one into page.evaluate.
//
// What that does NOT mean, and what an earlier version of this comment claimed
// wrongly: the key is not absent from the worker. It is ordinary job-level
// environment, playwright.chat-coverage.config.ts reads SUPABASE_SERVICE_ROLE_KEY
// to decide whether the live projects can run, and the spawn below forwards the
// whole of process.env to the child. Anything in this process that dumps
// process.env dumps the key with it. Only its USE is isolated.

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

export interface ChatSessionOptions {
  /** An existing account. Its password is neither needed nor modified. */
  email: string;
  /** Console origin: where the minted Supabase session is valid. */
  consoleUrl: string;
  /** Chat origin (Open WebUI). */
  chatUrl: string;
}

/**
 * Signs a context in to the chat surface and returns a page already on it.
 *
 * The only implementation of the chat hop. live-auth.mjs deliberately does not
 * carry a second one: the mint has to stay in a child process to keep the
 * service-role key out of this worker's module graph, but the hop itself is
 * pure browser driving with no credential in it, so it belongs on the side
 * that can be imported from a spec. Two copies of a login path is how a repo
 * ends up with one of them quietly broken.
 *
 * A mint alone does not sign in to Open WebUI. The minted cookies authenticate
 * the console origin; Open WebUI keeps its own session cookie that only its
 * OAuth callback can set, so a context carrying only the minted session lands
 * on /auth and reads as a broken login. This carries the session through the
 * real "Continue with Hive" hop, which is what a user does.
 */
export async function signInToChat(
  context: BrowserContext,
  options: ChatSessionOptions,
): Promise<Page> {
  try {
    return await walkChatHop(context, options);
  } catch (err: unknown) {
    // Playwright writes the URLs it navigated through into a waitForURL
    // timeout, and this function walks an OAuth callback, so the framework's
    // own failure text can carry a live `code` and `state`. That text reaches
    // the JSON report, the HTML report and the CI log, none of which any
    // per-URL redaction can reach after the fact. Redact here, where the
    // credential is still inside a string this code owns.
    throw new Error(redactText(err instanceof Error ? err.message : String(err)));
  }
}

async function walkChatHop(
  context: BrowserContext,
  options: ChatSessionOptions,
): Promise<Page> {
  await reauthenticate(context, { email: options.email, targetUrl: options.consoleUrl });
  const page = await context.newPage();
  await page.goto(`${options.consoleUrl}/console`, {
    waitUntil: "domcontentloaded",
    timeout: 60_000,
  });
  if (new URL(page.url()).pathname.startsWith("/auth")) {
    throw new Error("live-auth: the console rejected the minted session");
  }

  await page.goto(options.chatUrl, { waitUntil: "domcontentloaded", timeout: 60_000 });
  // The sign-in page hydrates late and animates continuously: a click dispatched
  // before hydration lands on a button with no handler bound and silently does
  // nothing, and Playwright's click-stability check never settles on it.
  await page.waitForTimeout(4000);
  const hive = page.getByRole("button", { name: /continue with hive/i });
  if (await hive.isVisible().catch(() => false)) {
    await hive.dispatchEvent("click");
    await page.waitForTimeout(8000);
  }
  // Supabase auto-approves a client the user has already granted, so consent
  // shows at most once per user and client and must stay optional.
  const approve = page.getByRole("button", { name: /approve/i });
  if (await approve.isVisible().catch(() => false)) {
    await approve.click().catch(() => {});
    await page.waitForTimeout(8000);
  }

  // Origin alone is not enough: the sign-in page is on the chat origin too, so
  // this predicate used to be satisfied by still sitting on /auth, and the
  // failure only surfaced later as a sweep that could not find the chat home.
  await page.waitForURL(
    (u) => u.origin === new URL(options.chatUrl).origin && !u.pathname.startsWith("/auth"),
    { timeout: 90_000 },
  );
  // "New Chat" is a button while the sidebar is collapsed and a link once it is
  // open, so a role-specific wait passes on a fresh profile and times out on a
  // returning one.
  await page
    .getByRole("button", { name: /new chat/i })
    .or(page.getByRole("link", { name: /new chat/i }))
    .first()
    .waitFor({ timeout: 90_000 });
  return page;
}
