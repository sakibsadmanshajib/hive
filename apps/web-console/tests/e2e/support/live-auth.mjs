// Live session helper: the one audited way for an automated run to obtain a
// real, signed-in session on a deployed Hive environment.
//
// WHY THIS EXISTS
// ---------------
// Every automated pass at the deployed surfaces needs a signed-in browser, and
// every ad-hoc attempt at that has reached for the same shortcut: overwrite a
// shared account's password through the Supabase admin API, then sign in with
// the value it just chose. On 2026-08-08 that rotated the demo account's
// password mid-run and invalidated it for three agents working concurrently.
// A password is shared mutable state on a shared account; a test run must
// never write to it.
//
// This module mints a session the way a magic-link login does, which requires
// no knowledge of the password and changes it in no way:
//
//   1. POST /auth/v1/admin/generate_link  (service role) -> a one-shot,
//      single-use token hash for an EXISTING account, addressed by email. It
//      does not create, list, or modify the account. Notably it does not call
//      GET /auth/v1/admin/users, which has been returning intermittent 500
//      "Database error finding users" (issue #791) and blocked four separate
//      runs.
//   2. POST /auth/v1/verify  (anon key) -> exchanges that one-shot token for a
//      normal access_token/refresh_token pair, and consumes it.
//
// What it writes to auth.users: nothing but the transient one-time token
// column that step 2 immediately clears, plus the ordinary sign-in
// timestamps (updated_at, last_sign_in_at, recovery_sent_at) that any login
// moves. Verified live on 2026-08-08: md5(encrypted_password) for the demo
// account was byte-identical before and after a full mint, and both
// confirmation_token and recovery_token were empty again afterwards. See
// docs/live-test-auth.md for the recorded before/after.
//
// FORBIDDEN, permanently: setting, resetting or rotating a password to obtain
// a session. There is no fallback path in here that does it, and adding one
// re-opens the incident above. If a mint fails, this module throws.
//
// USAGE
// -----
// As a Playwright storage-state producer (specs start already signed in):
//
//   // some.setup.ts
//   import { writeStorageState } from "./support/live-auth.mjs";
//   await writeStorageState({
//     email: process.env.HIVE_QA_AGENT_EMAIL,
//     targetUrl: "https://chat-hive.scubed.co/agent-workspace",
//     statePath: "tests/e2e/.auth/agent-workspace.json",
//   });
//   // playwright.config.ts: use: { storageState: "tests/e2e/.auth/agent-workspace.json" }
//
// Mid-run re-authentication (issue #782: a chat session dies roughly 55
// minutes after sign-in because a token refresh destroys the OAuth session;
// fix in PR #787, unmerged). A long run must renew rather than report a dead
// session as a broken control:
//
//   import { reauthenticate } from "./support/live-auth.mjs";
//   await reauthenticate(page.context(), { email, targetUrl });
//   await page.reload();
//
// Standalone, from a shell:
//
//   node tests/e2e/support/live-auth.mjs \
//     "e2e-verified+$E2E_RUN_KEY@hive-e2e.invalid" \
//     https://chat-hive.scubed.co/agent-workspace \
//     tests/e2e/.auth/agent-workspace.json
//
// The address above is run scoped on purpose, and this example used to name
// the shared demo account. Minting for that account is now refused unless the
// run declares itself read only (assertNotSharedDemoAccount below, or
// --read-only on this CLI).
//
// Requires SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY and SUPABASE_ANON_KEY.
// Nothing this module prints contains a token, a password or a key: every
// message goes through redactSecrets (e2e-fixture-seed.mjs), which scrubs
// credentials in URL fragments as well as query strings. Safe to run verbose.

import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { createBrowserClient } from "@supabase/ssr";
import { redactSecrets } from "./e2e-fixture-seed.mjs";

function requiredEnv(name) {
  const value = (process.env[name] ?? "").trim();
  if (!value) {
    throw new Error(
      `live-auth: ${name} is not set. Minting a session needs SUPABASE_URL, ` +
        "SUPABASE_SERVICE_ROLE_KEY and SUPABASE_ANON_KEY. There is no " +
        "credential-rotating fallback and there must never be one."
    );
  }
  return value;
}

function fail(stage, detail) {
  throw new Error(
    redactSecrets(
      `live-auth: ${stage} failed: ${detail}\n` +
        "Do NOT work around this by resetting the account password: that is " +
        "shared mutable state and rotating it invalidates every concurrent " +
        "run (2026-08-08 incident). Fix the mint instead."
    )
  );
}

async function postJson(url, headers, body) {
  const response = await fetch(url, {
    method: "POST",
    headers: { ...headers, "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const text = await response.text();
  let parsed = null;
  try {
    parsed = JSON.parse(text);
  } catch {
    parsed = null;
  }
  return { ok: response.ok, status: response.status, body: parsed, text };
}

// The shared account the owner demonstrates to prospects. An address, not a
// credential, so it may live in a tracked file (docs/live-test-auth.md).
export const SHARED_DEMO_ACCOUNT = "demo@hive-demo.invalid";

/**
 * Refuses a session for the shared demo account unless the caller has declared
 * the run read only.
 *
 * docs/live-test-auth.md has said since it was written that a write-capable
 * suite must never authenticate as this account, because every chat it sends
 * and every task it submits lands permanently in the sidebar the owner is
 * about to show someone. Nothing enforced that. The account accumulated 24
 * conversations of automation text, five of them on the day this guard was
 * written, and the reason nobody cleaned up is that the same document also
 * claimed the rows were undeletable (issues #848, #916).
 *
 * This is the one door every live session already passes through, so the rule
 * is checked once here rather than in each suite that could forget it.
 *
 * ponytail: a declaration gate, not a write blocker. It cannot stop a run that
 * declares itself read only from then writing; what it removes is the silent
 * default, so pointing a suite at this account becomes a deliberate, greppable
 * act instead of an accident. A real write blocker would need a per-request
 * proxy, which is a great deal of machinery for a rule one line can state.
 *
 * The declaration is per call, and deliberately not an environment variable.
 * An env var belongs to whatever set it, so one line in a workflow's `env:`
 * block would switch this off for every step in the job, invisibly and for
 * reasons unrelated to the suite that inherits it. A `readOnly` argument, or
 * the CLI's `--read-only` flag, sits at the call site where a reviewer reads
 * it.
 *
 * @param {string} email
 * @param {{ readOnly?: boolean }} [options]
 */
export function assertNotSharedDemoAccount(email, { readOnly = false } = {}) {
  if (String(email ?? "").trim().toLowerCase() !== SHARED_DEMO_ACCOUNT) {
    return;
  }
  if (readOnly) {
    return;
  }
  throw new Error(
    `live-auth: refusing to mint a session for ${SHARED_DEMO_ACCOUNT}. This is ` +
      "the shared account the owner demos to prospects, and anything a run " +
      "creates on it (a chat, an agent task, an API key) is visible on that " +
      "surface. Run as a dedicated E2E_RUN_KEY-scoped identity instead. If " +
      "this run only reads, say so at the call site with readOnly: true, or " +
      "--read-only on this module's CLI. See docs/live-test-auth.md."
  );
}

/**
 * Mints a live session for an existing account. Changes no credential.
 *
 * @param {{ email: string, supabaseUrl?: string, serviceRoleKey?: string, anonKey?: string, readOnly?: boolean }} options
 * @returns {Promise<{ access_token: string, refresh_token: string, expires_at: number, userId: string }>}
 */
export async function mintSession({
  email,
  supabaseUrl = requiredEnv("SUPABASE_URL"),
  serviceRoleKey = requiredEnv("SUPABASE_SERVICE_ROLE_KEY"),
  anonKey = requiredEnv("SUPABASE_ANON_KEY"),
  readOnly = false,
} = {}) {
  if (!email) {
    throw new Error("live-auth: mintSession needs an email for an existing account");
  }
  assertNotSharedDemoAccount(email, { readOnly });
  const base = supabaseUrl.replace(/\/+$/, "");

  // GoTrue keeps ONE outstanding one-time token per user, so two mints for the
  // same account that interleave leave the first holding a token the second
  // already replaced, and its verify answers 403 "Email link is invalid or has
  // expired". Observed live on 2026-08-08. One retry clears the interleave.
  //
  // ponytail: one retry, not a lock. Give each parallel worker its own account
  // if a suite ever mints for one account from several workers at once; no
  // amount of retrying fixes a genuinely concurrent stream of mints.
  for (let attempt = 0; ; attempt++) {
    try {
      return await mintOnce({ base, email, serviceRoleKey, anonKey });
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      if (attempt >= 1 || !/invalid or has expired/i.test(message)) {
        throw error;
      }
    }
  }
}

async function mintOnce({ base, email, serviceRoleKey, anonKey }) {

  // Addressed by email, so no admin user-listing call is involved (#791).
  const link = await postJson(
    `${base}/auth/v1/admin/generate_link`,
    { apikey: serviceRoleKey, Authorization: `Bearer ${serviceRoleKey}` },
    { type: "magiclink", email }
  );
  if (!link.ok) {
    fail(
      `generate_link for ${email} (HTTP ${link.status})`,
      link.body?.msg ?? link.body?.error_description ?? link.text.slice(0, 300)
    );
  }
  // GoTrue returns these flat; supabase-js nests them under `properties`.
  const properties = link.body?.properties ?? link.body ?? {};
  const tokenHash = properties.hashed_token;
  if (!tokenHash) {
    fail(
      "generate_link",
      "response carried no hashed_token, so there is nothing to exchange"
    );
  }

  // Exchanged over POST, whose response body carries the session as JSON. The
  // GET form of this endpoint answers with a redirect that puts the session in
  // the URL fragment instead, which is far easier to leak into a log or a
  // screenshot; there is no reason to take that risk here.
  const verified = await postJson(
    `${base}/auth/v1/verify`,
    { apikey: anonKey },
    { type: "magiclink", token_hash: tokenHash }
  );
  if (!verified.ok || !verified.body?.access_token) {
    fail(
      `verify for ${email} (HTTP ${verified.status})`,
      verified.body?.msg ??
        verified.body?.error_description ??
        verified.text.slice(0, 300)
    );
  }

  return {
    access_token: verified.body.access_token,
    refresh_token: verified.body.refresh_token,
    expires_at: verified.body.expires_at ?? Math.floor(Date.now() / 1000) + 3600,
    userId: verified.body.user?.id ?? "",
  };
}

/**
 * Encodes a session into the cookies the apps actually read.
 *
 * The encoding (name, base64url envelope, 3180-byte chunking) is produced by
 * @supabase/ssr itself rather than reimplemented here, by handing
 * createBrowserClient a cookie jar and letting it persist the session. Both
 * web-console and agent-console create their browser clients the same way, so
 * whatever this returns is by construction what those apps expect, including
 * after a future @supabase/ssr upgrade changes the format.
 *
 * @param {{ access_token: string, refresh_token: string }} session
 * @param {string} targetUrl an URL on the app origin the cookies are for
 * @returns {Promise<Array<Record<string, unknown>>>} Playwright cookie objects
 */
export async function sessionCookies(
  session,
  targetUrl,
  { supabaseUrl = requiredEnv("SUPABASE_URL"), anonKey = requiredEnv("SUPABASE_ANON_KEY") } = {}
) {
  const jar = new Map();
  const client = createBrowserClient(supabaseUrl, anonKey, {
    // Node has no `document`, so @supabase/ssr would otherwise reach for one.
    // isSingleton:false also keeps two mints in one process from sharing a
    // client and overwriting each other's session.
    isSingleton: false,
    cookies: {
      getAll: () =>
        [...jar.entries()].map(([name, entry]) => ({ name, value: entry.value })),
      setAll: (cookies) => {
        for (const cookie of cookies) {
          jar.set(cookie.name, { value: cookie.value, options: cookie.options ?? {} });
        }
      },
    },
  });
  const { error } = await client.auth.setSession({
    access_token: session.access_token,
    refresh_token: session.refresh_token,
  });
  if (error) {
    fail("setSession", error.message);
  }
  if (jar.size === 0) {
    fail(
      "setSession",
      "@supabase/ssr persisted no cookies, so the browser would still be signed out"
    );
  }

  const url = new URL(targetUrl);
  const now = Math.floor(Date.now() / 1000);
  return [...jar.entries()].map(([name, entry]) => ({
    name,
    value: entry.value,
    domain: url.hostname,
    path: entry.options.path ?? "/",
    expires: entry.options.maxAge ? now + Number(entry.options.maxAge) : -1,
    httpOnly: false,
    secure: url.protocol === "https:",
    sameSite: "Lax",
  }));
}

/**
 * Mints a session and writes a Playwright storage state file for it.
 *
 * @param {{ email: string, targetUrl: string, statePath: string }} options
 * @returns {Promise<{ statePath: string, userId: string, expiresAt: number }>}
 */
export async function writeStorageState({ email, targetUrl, statePath, ...rest }) {
  const session = await mintSession({ email, ...rest });
  const cookies = await sessionCookies(session, targetUrl, rest);
  const absolute = resolve(statePath);
  mkdirSync(dirname(absolute), { recursive: true });
  writeFileSync(absolute, JSON.stringify({ cookies, origins: [] }, null, 2) + "\n");
  return { statePath: absolute, userId: session.userId, expiresAt: session.expires_at };
}

/**
 * Signs an already-open browser context in, by installing a freshly minted
 * session's cookies on it.
 *
 * @param {{ addCookies: (cookies: Array<Record<string, unknown>>) => Promise<void> }} context
 * @param {{ email: string, targetUrl: string }} options
 * @returns {Promise<{ userId: string, expiresAt: number }>}
 */
export async function reauthenticate(context, { email, targetUrl, ...rest }) {
  const session = await mintSession({ email, ...rest });
  await context.addCookies(await sessionCookies(session, targetUrl, rest));
  return { userId: session.userId, expiresAt: session.expires_at };
}

/**
 * Runs `action`, and if it throws while the session looks dead, mints a new
 * one and runs it once more.
 *
 * A long run outlives its session: issue #782 has a chat session die roughly
 * 55 minutes after sign-in because a token refresh destroys the OAuth session
 * (fix in PR #787, unmerged). Without this, the second hour of a run reports
 * working controls as broken ones. Deliberately one retry: a control that is
 * genuinely broken must still fail.
 *
 * @template T
 * @param {{ addCookies: (cookies: Array<Record<string, unknown>>) => Promise<void> }} context
 * @param {{ email: string, targetUrl: string }} options
 * @param {() => Promise<T>} action
 * @returns {Promise<T>}
 */
export async function withLiveSession(context, options, action) {
  try {
    return await action();
  } catch {
    await reauthenticate(context, options);
    return await action();
  }
}

// --- CLI -------------------------------------------------------------------
// node tests/e2e/support/live-auth.mjs <email> <targetUrl> [statePath]
//
// Kept inside a function rather than at module top level on purpose: Playwright
// loads spec dependencies through a CommonJS transform, and a top-level `await`
// anywhere in this file makes every spec that imports it fail to load with
// "require() cannot be used on an ESM graph with top-level await".
async function main() {
  const argv = process.argv.slice(2);
  // Order-independent so the flag can sit anywhere after the command.
  const readOnly = argv.includes("--read-only");
  const [email, targetUrl, statePath] = argv.filter((a) => a !== "--read-only");
  if (!email || !targetUrl) {
    console.error(
      "usage: node tests/e2e/support/live-auth.mjs [--read-only] <email> <targetUrl> [statePath]\n" +
        "  --read-only  assert this run only reads, which is the sole way to " +
        "mint for the shared demo account"
    );
    process.exit(2);
  }
  if (statePath) {
    const result = await writeStorageState({ email, targetUrl, statePath, readOnly });
    // No token material: an id, a path and an expiry are the whole output.
    console.log(
      redactSecrets(
        `live-auth: wrote storage state for ${email} (user ${result.userId}) to ${result.statePath}; expires_at=${result.expiresAt}`
      )
    );
    return;
  }
  const session = await mintSession({ email, readOnly });
  console.log(
    redactSecrets(
      `live-auth: minted a session for ${email} (user ${session.userId}); expires_at=${session.expires_at}`
    )
  );
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
  main().catch((error) => {
    console.error(redactSecrets(error instanceof Error ? error.message : String(error)));
    process.exit(1);
  });
}
