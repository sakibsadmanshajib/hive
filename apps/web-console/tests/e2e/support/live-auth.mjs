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
// Requires SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY and SUPABASE_ANON_KEY, plus
// SUPABASE_ADMIN_URL wherever the admin API is not served on SUPABASE_URL (see
// adminOrigin below).
//
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

// The origin that answers GoTrue's ADMIN routes, which is not always the origin
// the browser holds a session for.
//
// deploy/docker/Caddyfile.supabase refuses /auth/v1/admin/* at the PUBLIC
// listener on purpose: the service-role key is used server side and in network
// only, so a leaked one must not be usable from the internet. The admin API is
// served on the in-network listener alone. A run positioned inside that network
// therefore points SUPABASE_ADMIN_URL at it and leaves SUPABASE_URL as the
// browser-facing origin, because the two values are used for different things:
// mintSession talks to the admin API, while sessionCookies has to key the
// cookie envelope on the host each app was BUILT with
// (`sb-<first-hostname-label>-auth-token`, SupabaseClient.ts:294). One value
// cannot be right for both once they differ, and the failure if they are
// conflated is silent: the mint succeeds and the app still reads as signed
// out. sso-wave1-fixture.mjs already makes this same split by hand.
//
// Unset, this is SUPABASE_URL and nothing changes for any existing caller.
function adminOrigin() {
  return (process.env.SUPABASE_ADMIN_URL ?? "").trim() || requiredEnv("SUPABASE_URL");
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

// Base addresses that accumulate automation litter when reused outside their
// own run (issue #1476's four-row table: `e2e-verified+qafunded-...@` is a
// `+`-tagged instance of the `e2e-verified@scubed.com.bd` base below, not a
// fourth base). Addresses, not credentials, so they may live in a tracked file
// (docs/live-test-auth.md).
export const PROTECTED_ACCOUNT_BASES = Object.freeze([
  "demo@hive-demo.invalid",
  "qa-tester@hive.test",
  "e2e-verified@scubed.com.bd",
]);

// A key shorter than this is guessable by accident: single common words
// ("hive", "test", "demo", "e2e") were each a substring of some protected
// base, which is exactly the bypass this floor closes alongside the exact-tag
// match below. github.run_id-run_attempt and $(whoami)-$(date +%s) (the two
// shapes E2E_RUN_KEY actually takes in this repo) are both well past it.
export const MIN_RUN_KEY_LENGTH = 8;

// Trims the same character class Python's guard trims (shared_demo_account.py
// _normalise), rather than relying on each language's own idea of
// "whitespace": JS's String.prototype.trim() folds in U+FEFF and Python's
// str.strip() does not, while Python's folds in the C0 control range
// (U+001C included) and JS's does not. Either disagreement lets the same
// address be refused by one entry point and allowed by the other.
const EDGE_CHARS = /^[\x00-\x20\x7f﻿]+|[\x00-\x20\x7f﻿]+$/g;

function normaliseEmail(email) {
  return String(email ?? "").replace(EDGE_CHARS, "").toLowerCase();
}

// `local+tag@domain` -> `local@domain`. GoTrue does not fold the tag away
// itself, so a stale or hardcoded tag on a protected base is still that base;
// this is what lets the run-key check below see through it.
function baseAddress(normalisedEmail) {
  const at = normalisedEmail.indexOf("@");
  if (at === -1) {
    return normalisedEmail;
  }
  const local = normalisedEmail.slice(0, at).split("+")[0];
  return `${local}${normalisedEmail.slice(at)}`;
}

// The `+tag` component of the local part only ("local+TAG@domain" -> "TAG"),
// or "" when there is none. The domain never participates: a substring test
// against the whole address let `E2E_RUN_KEY=-` open every protected base at
// once, since "-" (and plenty of other short keys) occurs somewhere in nearly
// any address. Comparison against this tag is exact, never substring.
function localTag(normalisedEmail) {
  const at = normalisedEmail.indexOf("@");
  const local = at === -1 ? normalisedEmail : normalisedEmail.slice(0, at);
  const plus = local.indexOf("+");
  return plus === -1 ? "" : local.slice(plus + 1);
}

/**
 * Refuses a session for a protected base address unless the caller has
 * declared the run read only, or the address is scoped to this run's
 * `E2E_RUN_KEY`.
 *
 * docs/live-test-auth.md has said since it was written that a write-capable
 * suite must never authenticate as the shared demo account, because every
 * chat it sends and every task it submits lands permanently in the sidebar
 * the owner is about to show someone. Nothing enforced that. The account
 * accumulated 24 conversations of automation text, five of them on the day
 * this guard was written (issues #848, #916). Three more shared accounts
 * carry the same accumulation cause (issue #1476), which is why this checks
 * a base address rather than one literal.
 *
 * This is the one door every live session already passes through, so the rule
 * is checked once here rather than in each suite that could forget it.
 *
 * ponytail: a declaration gate, not a write blocker. It cannot stop a run that
 * declares itself read only from then writing; what it removes is the silent
 * default, so pointing a suite at one of these accounts becomes a deliberate,
 * greppable act instead of an accident. A real write blocker would need a
 * per-request proxy, which is a great deal of machinery for a rule one line
 * can state.
 *
 * The `readOnly` declaration is per call, and deliberately not an environment
 * variable: one line in a workflow's `env:` block would switch it off for
 * every step in the job, invisibly and for reasons unrelated to the suite
 * that inherits it. `E2E_RUN_KEY` is read from the environment here rather
 * than accepted as an argument, on purpose: it is the run's own identity, not
 * a per-call claim a caller could set to whatever makes the check pass, and
 * every run-scoped fixture address is already built from the same value
 * (`e2e-fixture-seed.mjs`'s `runScopedEmail`, `e2e-auth-creds.ts`).
 *
 * Accepts a missing address rather than throwing on one: mintSession rejects an
 * empty email itself, with a message about the missing argument, and this guard
 * has no business pre-empting that with an unrelated one. Hence the union type,
 * which is what the body already handles.
 *
 * @param {string | undefined | null} email
 * @param {{ readOnly?: boolean }} [options]
 */
export function assertNotSharedDemoAccount(email, { readOnly = false } = {}) {
  const normalised = normaliseEmail(email);
  if (!normalised || !PROTECTED_ACCOUNT_BASES.includes(baseAddress(normalised))) {
    return;
  }
  if (readOnly) {
    return;
  }
  const runKey = normaliseEmail(process.env.E2E_RUN_KEY);
  if (runKey.length >= MIN_RUN_KEY_LENGTH && localTag(normalised) === runKey) {
    return;
  }
  throw new Error(
    `live-auth: refusing to mint a session for ${normalised}. It is one of ` +
      "the shared accounts that accumulates automation litter when reused " +
      "outside its own run (issues #848, #916, #1476). Run as a dedicated " +
      `identity carrying E2E_RUN_KEY as a "+tag" on the local part instead ` +
      `("local+$E2E_RUN_KEY@domain", at least ${MIN_RUN_KEY_LENGTH} ` +
      "characters). If this run only reads, say so at the call site with " +
      "readOnly: true, or --read-only on this module's CLI. See " +
      "docs/live-test-auth.md."
  );
}

/**
 * Mints a live session for an existing account. Changes no credential.
 *
 * `supabaseUrl` is the ADMIN origin (SUPABASE_ADMIN_URL, falling back to
 * SUPABASE_URL), which is not necessarily the origin the browser talks to.
 * See adminOrigin above.
 *
 * @param {{ email: string, supabaseUrl?: string, serviceRoleKey?: string, anonKey?: string, readOnly?: boolean }} options
 * @returns {Promise<{ access_token: string, refresh_token: string, expires_at: number, userId: string }>}
 */
export async function mintSession({
  email,
  supabaseUrl = adminOrigin(),
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
