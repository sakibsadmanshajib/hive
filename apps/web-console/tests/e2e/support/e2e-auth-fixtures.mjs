import { readFileSync } from "node:fs";
import { pathToFileURL, fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

// Shared defaults with `e2e-auth-creds.ts`. Both modules read the same JSON
// so the spec-side env lookup and the fixture CLI cannot drift apart.
const DEFAULTS = JSON.parse(
  readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), "e2e-auth-defaults.json"),
    "utf8"
  )
);

function envOrDefault(name, fallback, { minLength = 0, validator } = {}) {
  const raw = process.env[name];
  if (raw === undefined || raw === "") {
    return fallback;
  }
  if (minLength > 0 && raw.length < minLength) {
    console.warn(
      `[e2e-auth-fixtures] ${name} is set but too short (${raw.length} < ${minLength}); using fallback`
    );
    return fallback;
  }
  if (validator && !validator(raw)) {
    console.warn(
      `[e2e-auth-fixtures] ${name} is set but failed validation; using fallback`
    );
    return fallback;
  }
  return raw;
}

function isValidEmail(value) {
  return value.length >= 5 && value.includes("@") && value.includes(".");
}

// Resolved credentials — mirror `e2e-auth-creds.ts` exactly so the spec side
// (which imports those constants) and this CLI always agree.
export const E2E_VERIFIED_EMAIL = envOrDefault(
  "E2E_VERIFIED_EMAIL",
  DEFAULTS.verifiedEmail,
  { validator: isValidEmail }
);
export const E2E_UNVERIFIED_EMAIL = envOrDefault(
  "E2E_UNVERIFIED_EMAIL",
  DEFAULTS.unverifiedEmail,
  { validator: isValidEmail }
);

function maskEmail(value) {
  const [local = "", domain = ""] = value.split("@");
  const head = local.slice(0, 3);
  return `${head}***@${domain}`;
}

function hasEdgeFunctionEnv() {
  return (
    Boolean(process.env.E2E_FIXTURE_URL) &&
    Boolean(process.env.E2E_FIXTURE_SECRET)
  );
}

export async function prepareE2EAuthFixtures() {
  const edgeEnv = hasEdgeFunctionEnv();
  if (process.env.E2E_FIXTURE_VERBOSE === "1") {
    console.log(
      `[e2e-auth-fixtures] mode=${edgeEnv ? "edge-function" : "skipped"} verifiedEmail=${maskEmail(
        E2E_VERIFIED_EMAIL
      )} unverifiedEmail=${maskEmail(E2E_UNVERIFIED_EMAIL)}`
    );
  }

  // The `e2e-fixtures` Supabase Edge Function owns all admin-API work
  // server-side. See `supabase/functions/e2e-fixtures/` for the deploy
  // contract. Without it, the fixture is a no-op — seeding must be run
  // out-of-band (e.g. `supabase functions invoke e2e-fixtures`).
  if (!edgeEnv) {
    return;
  }

  const url = process.env.E2E_FIXTURE_URL;
  const secret = process.env.E2E_FIXTURE_SECRET;
  const response = await fetch(url, {
    method: "POST",
    headers: {
      "X-E2E-Secret": secret,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ action: "reset" }),
  });
  const text = await response.text();
  let data = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { raw: text };
  }
  if (!response.ok) {
    const message = data?.error ?? `${response.status} ${response.statusText}`;
    throw new Error(`e2e-fixtures edge function failed: ${message}`);
  }
  return data;
}

// =============================================================================
// Phase 14 — HANDOFF-13-01 closure: seedSecondaryWorkspace
// =============================================================================
//
// `auth-shell.spec.ts:88` ("workspace switcher persists selected account")
// currently `test.skip()`-s itself when the verified tester only has one
// workspace, masking a coverage gap (CONSOLE-13-01). The Phase 14 fix is this
// fixture extension: the spec calls `seedSecondaryWorkspace(ownerEmail)`,
// which uses the e2e-fixtures Supabase Edge Function's `seed-workspace`
// action to provision a second account + membership for the verified tester
// and returns the new account UUID. The spec then re-runs green.
//
// When `E2E_FIXTURE_URL`/`E2E_FIXTURE_SECRET` are absent, the function is a
// no-op (returns null) — matches the rest of this file's contract where
// fixture mutations only happen when the edge-function env is wired.

export async function seedSecondaryWorkspace(ownerEmail) {
  if (!hasEdgeFunctionEnv()) {
    return null;
  }
  const url = process.env.E2E_FIXTURE_URL;
  const secret = process.env.E2E_FIXTURE_SECRET;
  const response = await fetch(url, {
    method: "POST",
    headers: {
      "X-E2E-Secret": secret,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      action: "seed-workspace",
      owner_email: ownerEmail,
      display_name: "Phase 14 secondary",
    }),
  });
  const text = await response.text();
  let data = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { raw: text };
  }
  if (!response.ok) {
    const message = data?.error ?? `${response.status} ${response.statusText}`;
    throw new Error(`seedSecondaryWorkspace failed: ${message}`);
  }
  // Edge function may legitimately return { workspaceID, jwt } OR a no-op
  // payload when the secondary workspace already exists. Normalise to the
  // documented return shape.
  if (data && typeof data === "object" && data.workspaceID) {
    return { workspaceID: String(data.workspaceID), jwt: String(data.jwt ?? "") };
  }
  return null;
}

// =============================================================================
// Phase 14 — HANDOFF-13-02 closure: resetProfileBetweenSpecs
// =============================================================================
//
// `profile-completion.spec.ts:71` ("dashboard shows setup reminder ...")
// expects the verified tester's `profile_setup_complete` to be `false` so
// that the dashboard renders the "Complete setup" CTA. The previous test in
// the file (`setup saves profile`) sets it to `true`, polluting the second
// test's preconditions when the file's `beforeEach` reset doesn't include
// the profile flag.
//
// `resetProfileBetweenSpecs(testInfo)` explicitly resets the profile flag
// for the verified tester via the e2e-fixtures Edge Function's
// `reset-profile` action. Specs call it from `test.beforeEach` to guarantee
// independence regardless of file order or worker count.

export async function resetProfileBetweenSpecs(testInfo) {
  if (!hasEdgeFunctionEnv()) {
    return null;
  }
  const url = process.env.E2E_FIXTURE_URL;
  const secret = process.env.E2E_FIXTURE_SECRET;
  const targetEmail =
    testInfo && typeof testInfo === "object" && testInfo.email
      ? testInfo.email
      : E2E_VERIFIED_EMAIL;
  const response = await fetch(url, {
    method: "POST",
    headers: {
      "X-E2E-Secret": secret,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      action: "reset-profile",
      email: targetEmail,
    }),
  });
  const text = await response.text();
  let data = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { raw: text };
  }
  if (!response.ok) {
    const message = data?.error ?? `${response.status} ${response.statusText}`;
    throw new Error(`resetProfileBetweenSpecs failed: ${message}`);
  }
  return data;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  prepareE2EAuthFixtures()
    .then((summary) => {
      if (summary && process.env.E2E_FIXTURE_VERBOSE === "1") {
        console.log(JSON.stringify(summary, null, 2));
      }
    })
    .catch((error) => {
      console.error(error instanceof Error ? error.message : error);
      process.exitCode = 1;
    });
}
