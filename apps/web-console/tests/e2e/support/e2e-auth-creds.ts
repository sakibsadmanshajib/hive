// Source of truth for E2E test credentials shared between Playwright specs
// and the fixture CLI (`e2e-auth-fixtures.mjs`).
//
// Non-secret defaults (addresses and min-length constants) live in
// `e2e-auth-defaults.json` so both the TS module (consumed by specs) and the
// sibling ESM fixture entrypoint (consumed by `execFileSync` from
// `beforeEach`) read the same values.
//
// NO CREDENTIAL HAS A COMMITTED DEFAULT, and none may ever be added back.
// This repository is public, and the two passwords plus the invitation token
// used to sit in that JSON file in plaintext for live accounts that hold a
// tenant OWNER role, which the seeder then wrote back onto those accounts on
// every credential-less run. The three values below are therefore read from
// the environment only, and a missing one throws by name rather than falling
// back or skipping quietly: a silent fallback is what kept the committed
// values live for months. See docs/live-test-auth.md.
//
// The two addresses stay: an address is not a credential, and specs need a
// stable identity to seed. Env overrides are honored for them.

import defaults from "./e2e-auth-defaults.json" with { type: "json" };

type AuthDefaults = {
  minPasswordLength: number;
  minTokenLength: number;
  verifiedEmail: string;
  unverifiedEmail: string;
};

const DEFAULTS: AuthDefaults = defaults as AuthDefaults;

function isValidEmail(value: string): boolean {
  return value.length >= 5 && value.includes("@") && value.includes(".");
}

function envOrDefault(
  name: string,
  fallback: string,
  opts: { validator?: (value: string) => boolean } = {}
): string {
  const { validator } = opts;
  const raw = process.env[name];
  if (raw === undefined || raw === "") {
    return fallback;
  }
  if (validator && !validator(raw)) {
    console.warn(
      `[e2e-auth-creds] ${name} is set but failed validation; using fallback`
    );
    return fallback;
  }
  return raw;
}

// requiredSecretEnv is the only way a credential enters this suite. It throws
// instead of returning a fallback, and the message names the variable and how
// to set it, so a developer who runs the suite with nothing configured gets an
// instruction rather than a run that quietly seeds shared live accounts.
export function requiredSecretEnv(name: string, minLength: number): string {
  const raw = process.env[name] ?? "";
  if (raw === "") {
    throw new Error(
      `[e2e-auth-creds] ${name} is required and has no default. Set it in ` +
        "your shell (or as a CI secret) before running the E2E suite. " +
        "Credentials are never committed to this repository; see " +
        "docs/live-test-auth.md."
    );
  }
  if (raw.length < minLength) {
    throw new Error(
      `[e2e-auth-creds] ${name} is set but too short (${raw.length} < ${minLength}).`
    );
  }
  return raw;
}

// Every fixture address is scoped to the run, so a spec can only ever sign in
// as an account this run created. The shared addresses in the JSON file are
// bases to derive from, never targets: seeding writes a password to whatever
// address it is given, and writing to a shared account revokes the sessions
// every other run is holding. `runScopedEmail` in e2e-fixture-seed.mjs applies
// the identical rule on the seeding side, including the same idempotence for
// the already-namespaced address ci.yml passes.
export function runScopedEmail(email: string, runKey: string): string {
  if (runKey === "") {
    throw new Error(
      "[e2e-auth-creds] E2E_RUN_KEY is required and has no default. Without " +
        "it the fixtures target shared live accounts, and seeding overwrites " +
        "their passwords, which revokes every session other runs are holding " +
        "(see docs/live-test-auth.md). Export any unique value, for example " +
        "E2E_RUN_KEY=$(whoami)-$(date +%s)."
    );
  }
  if (email.includes(`+${runKey}@`)) {
    return email;
  }
  const at = email.indexOf("@");
  if (at === -1) {
    return `${email}+${runKey}`;
  }
  return `${email.slice(0, at)}+${runKey}${email.slice(at)}`;
}

export const E2E_RUN_KEY = (process.env.E2E_RUN_KEY ?? "").trim();

export const E2E_VERIFIED_EMAIL = runScopedEmail(
  envOrDefault("E2E_VERIFIED_EMAIL", DEFAULTS.verifiedEmail, {
    validator: isValidEmail,
  }),
  E2E_RUN_KEY
);
export const E2E_UNVERIFIED_EMAIL = runScopedEmail(
  envOrDefault("E2E_UNVERIFIED_EMAIL", DEFAULTS.unverifiedEmail, {
    validator: isValidEmail,
  }),
  E2E_RUN_KEY
);
export const E2E_VERIFIED_PASSWORD = requiredSecretEnv(
  "E2E_VERIFIED_PASSWORD",
  DEFAULTS.minPasswordLength
);
export const E2E_UNVERIFIED_PASSWORD = requiredSecretEnv(
  "E2E_UNVERIFIED_PASSWORD",
  DEFAULTS.minPasswordLength
);
export const E2E_INVITATION_TOKEN = requiredSecretEnv(
  "E2E_INVITATION_TOKEN",
  DEFAULTS.minTokenLength
);
