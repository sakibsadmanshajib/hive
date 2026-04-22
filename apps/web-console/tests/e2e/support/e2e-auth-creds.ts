// Source of truth for E2E test credentials shared between Playwright specs
// and the fixture CLI (`e2e-auth-fixtures.mjs`).
//
// Defaults + min-length constants live in `e2e-auth-defaults.json` so both
// the TS module (consumed by specs) and the sibling ESM fixture entrypoint
// (consumed by `execFileSync` from `beforeEach`) read the same values.
//
// Test-only fixtures target dedicated `e2e-*@hive-ci.test` accounts in the
// staging Supabase project. Env overrides (`E2E_*`) are honored when they
// meet minimum length checks; empty / unset env vars silently fall back to
// defaults, non-empty-but-too-short values emit a warning.

import defaults from "./e2e-auth-defaults.json" with { type: "json" };

type AuthDefaults = {
  minPasswordLength: number;
  minTokenLength: number;
  verifiedEmail: string;
  unverifiedEmail: string;
  verifiedPassword: string;
  unverifiedPassword: string;
  invitationToken: string;
};

const DEFAULTS: AuthDefaults = defaults as AuthDefaults;

function envOrDefault(
  name: string,
  fallback: string,
  minLength = 0
): string {
  const raw = process.env[name];
  if (raw === undefined || raw === "") {
    return fallback;
  }
  if (minLength > 0 && raw.length < minLength) {
    console.warn(
      `[e2e-auth-creds] ${name} is set but too short (${raw.length} < ${minLength}); using fallback`
    );
    return fallback;
  }
  return raw;
}

export const E2E_VERIFIED_EMAIL = envOrDefault(
  "E2E_VERIFIED_EMAIL",
  DEFAULTS.verifiedEmail
);
export const E2E_UNVERIFIED_EMAIL = envOrDefault(
  "E2E_UNVERIFIED_EMAIL",
  DEFAULTS.unverifiedEmail
);
export const E2E_VERIFIED_PASSWORD = envOrDefault(
  "E2E_VERIFIED_PASSWORD",
  DEFAULTS.verifiedPassword,
  DEFAULTS.minPasswordLength
);
export const E2E_UNVERIFIED_PASSWORD = envOrDefault(
  "E2E_UNVERIFIED_PASSWORD",
  DEFAULTS.unverifiedPassword,
  DEFAULTS.minPasswordLength
);
export const E2E_INVITATION_TOKEN = envOrDefault(
  "E2E_INVITATION_TOKEN",
  DEFAULTS.invitationToken,
  DEFAULTS.minTokenLength
);
