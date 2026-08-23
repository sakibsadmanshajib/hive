/**
 * Fixture for the SSO wave 1 e2e journeys.
 *
 * Runs INSIDE the Docker network (docker run --network ...) because GoTrue's
 * admin API is deliberately refused at the public edge listener; only the
 * internal caddy-supabase listener serves it. Creates a throwaway confirmed
 * user, mints a real session through the audited one-time-token flow
 * (tests/e2e/support/live-auth.mjs), and writes a Playwright storage state
 * whose ssr cookies are scoped to the console origin.
 *
 * Usage:
 *   node sso-wave1-fixture.mjs create <email> <consoleOrigin> <statePath>
 *   node sso-wave1-fixture.mjs cleanup <email> <userIdFile>
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

import { mintSession, sessionCookies } from "./live-auth.mjs";

async function postJson(url, key, body) {
  const response = await fetch(url, {
    method: "POST",
    headers: {
      apikey: key,
      Authorization: `Bearer ${key}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  let parsed = null;
  try {
    parsed = await response.json();
  } catch {
    parsed = null;
  }
  return { ok: response.ok, status: response.status, body: parsed };
}

function readString(source, field) {
  if (typeof source !== "object" || source === null) return null;
  const value = Reflect.get(source, field);
  return typeof value === "string" ? value : null;
}

const [, , command, email, consoleOrigin, statePath] = process.argv;

if (command === "create") {
  const base = (process.env.SUPABASE_URL ?? "").replace(/\/+$/, "");
  const serviceKey = process.env.SUPABASE_SERVICE_ROLE_KEY ?? "";
  const anonKey = process.env.SUPABASE_ANON_KEY ?? "";
  if (!base || !serviceKey || !anonKey) {
    throw new Error("fixture needs SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY, SUPABASE_ANON_KEY");
  }
  const created = await postJson(`${base}/auth/v1/admin/users`, serviceKey, {
    email,
    email_confirm: true,
  });
  if (!created.ok && created.status !== 422) {
    throw new Error(`user create failed HTTP ${created.status}`);
  }
  const userId = readString(created.body, "id");
  const session = await mintSession({ email, supabaseUrl: base, anonKey });
  // Cookie NAMES derive from the supabase URL host each app was built with
  // (`sb-<first-hostname-label>-auth-token`, SupabaseClient.ts:294), so the
  // envelope must be encoded against the console origin even though the
  // session itself was minted over the internal listener.
  const cookies = await sessionCookies(session, consoleOrigin, {
    supabaseUrl: consoleOrigin,
  });
  mkdirSync(dirname(statePath), { recursive: true });
  writeFileSync(statePath, JSON.stringify({ email, userId, cookies }, null, 2) + "\n");
  process.stdout.write(
    `fixture: created ${email} (id ${userId ?? "unknown"}), state at ${statePath}\n`,
  );
}

if (command === "cleanup") {
  const base = (process.env.SUPABASE_URL ?? "").replace(/\/+$/, "");
  const serviceKey = process.env.SUPABASE_SERVICE_ROLE_KEY ?? "";
  if (!base || !serviceKey) {
    throw new Error("cleanup needs SUPABASE_URL and SUPABASE_SERVICE_ROLE_KEY");
  }
  const stateFile = consoleOrigin;
  const { readFileSync } = await import("node:fs");
  let userId = null;
  try {
    const parsedState = JSON.parse(readFileSync(stateFile, "utf8"));
    userId = readString(parsedState, "userId");
  } catch {
    userId = null;
  }
  if (userId) {
    const response = await fetch(`${base}/auth/v1/admin/users/${userId}`, {
      method: "DELETE",
      headers: { apikey: serviceKey, Authorization: `Bearer ${serviceKey}` },
    });
    process.stdout.write(`fixture: cleanup HTTP ${response.status}\n`);
  } else {
    process.stdout.write("fixture: cleanup skipped, no userId in state file\n");
  }
}
