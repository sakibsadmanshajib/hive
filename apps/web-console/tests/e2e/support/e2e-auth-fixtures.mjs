// Fixture CLI for the Playwright E2E suite.
//
// Resolves the credentials the specs will sign in with, then seeds exactly
// that identity through `e2e-fixture-seed.mjs`. Specs invoke this as a child
// process from `beforeEach` (see tests/e2e/auth-shell.spec.ts), so the
// service-role key stays in this process and never enters page context.
//
// Usage:
//   node tests/e2e/support/e2e-auth-fixtures.mjs
//   node tests/e2e/support/e2e-auth-fixtures.mjs reset-profile <email>
//
// Requires SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY, and (for the default
// seeding action) E2E_VERIFIED_PASSWORD, E2E_UNVERIFIED_PASSWORD and
// E2E_INVITATION_TOKEN. None has a
// fallback: seeding used to POST to a deployed `e2e-fixtures` Supabase Edge
// Function and quietly no-op when its URL and secret were absent, which is
// what let a caller change while the deployed seeder stayed behind, with
// nothing failing until a spec timed out signing in as a user nobody had
// created.

import { readFileSync } from "node:fs";
import { pathToFileURL, fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import {
  createAdminClient,
  redactSecrets,
  resetProfileComplete,
  seedFixtures,
} from "./e2e-fixture-seed.mjs";

// Shared non-secret defaults with `e2e-auth-creds.ts`. Both modules read the
// same JSON so the spec-side env lookup and this CLI cannot drift apart. That
// file carries addresses and length limits only. No credential has a committed
// default here or there, and none may be added back: this repository is
// public, and the values that used to live in that JSON were live ones this
// very CLI then wrote back onto shared accounts on every credential-less run.
const DEFAULTS = JSON.parse(
  readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), "e2e-auth-defaults.json"),
    "utf8"
  )
);

function envOrDefault(name, fallback, { validator } = {}) {
  const raw = process.env[name];
  if (raw === undefined || raw === "") {
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

// Mirrors requiredSecretEnv in `e2e-auth-creds.ts`. Throws rather than
// returning a fallback, so a run with nothing configured stops here with the
// name of the variable to set instead of seeding shared live accounts with a
// value that is public in this repository's history.
function requiredSecretEnv(name, minLength) {
  const raw = process.env[name] ?? "";
  if (raw === "") {
    throw new Error(
      `[e2e-auth-fixtures] ${name} is required and has no default. Set it in ` +
        "your shell (or as a CI secret) before seeding fixtures. Credentials " +
        "are never committed to this repository; see docs/live-test-auth.md."
    );
  }
  if (raw.length < minLength) {
    throw new Error(
      `[e2e-auth-fixtures] ${name} is set but too short (${raw.length} < ${minLength}).`
    );
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
// The three credentials are resolved inside prepareE2EAuthFixtures rather
// than at module scope, so the `reset-profile <email>` action (which needs no
// credential at all) still runs when they are unset.
// Set by CI to one value per job attempt (see .github/workflows/ci.yml),
// e.g. `${{ github.run_id }}-${{ github.run_attempt }}`. Empty for local and
// manual runs, which means "no run key": the seeder then uses the single
// shared fixture identity instead of a namespaced one.
export const E2E_RUN_KEY = process.env.E2E_RUN_KEY || undefined;

function maskEmail(value) {
  const [local = "", domain = ""] = value.split("@");
  const head = local.slice(0, 3);
  return `${head}***@${domain}`;
}

export async function prepareE2EAuthFixtures() {
  if (process.env.E2E_FIXTURE_VERBOSE === "1") {
    console.log(
      `[e2e-auth-fixtures] seeding verifiedEmail=${maskEmail(
        E2E_VERIFIED_EMAIL
      )} unverifiedEmail=${maskEmail(E2E_UNVERIFIED_EMAIL)}`
    );
  }

  // The credentials this process resolved are passed straight through, so the
  // seeder writes precisely the identity the specs will sign in as. One
  // derivation, one place.
  return seedFixtures(createAdminClient(), {
    runKey: E2E_RUN_KEY,
    verifiedEmail: E2E_VERIFIED_EMAIL,
    unverifiedEmail: E2E_UNVERIFIED_EMAIL,
    verifiedPassword: requiredSecretEnv(
      "E2E_VERIFIED_PASSWORD",
      DEFAULTS.minPasswordLength
    ),
    unverifiedPassword: requiredSecretEnv(
      "E2E_UNVERIFIED_PASSWORD",
      DEFAULTS.minPasswordLength
    ),
    invitationToken: requiredSecretEnv(
      "E2E_INVITATION_TOKEN",
      DEFAULTS.minTokenLength
    ),
  });
}

export async function resetProfileBetweenSpecs(email) {
  return resetProfileComplete(createAdminClient(), email || E2E_VERIFIED_EMAIL);
}

function runCli(argv) {
  const [action, ...rest] = argv;
  if (!action || action === "reset") {
    return prepareE2EAuthFixtures();
  }
  if (action === "reset-profile") {
    return resetProfileBetweenSpecs(rest[0]);
  }
  return Promise.reject(new Error(`unknown action: ${action}`));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  runCli(process.argv.slice(2))
    .then((summary) => {
      if (summary && process.env.E2E_FIXTURE_VERBOSE === "1") {
        console.log(JSON.stringify(summary, null, 2));
      }
    })
    .catch((error) => {
      // Callers relay this child process's stderr into the CI log on failure,
      // so scrub the service-role key out of anything a client library may
      // have embedded in the message before it is printed.
      console.error(
        redactSecrets(error instanceof Error ? error.message : error)
      );
      process.exitCode = 1;
    });
}
