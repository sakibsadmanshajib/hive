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
