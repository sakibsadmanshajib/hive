// Source of truth for E2E test credentials shared between Playwright specs
// and the fixture CLI (`e2e-auth-fixtures.mjs`). Keep values in sync.
//
// Test-only fixtures target dedicated `e2e-*@hive-ci.test` accounts in the
// staging Supabase project. Env overrides are honored when they meet validity
// checks (min password length = 6, min token length = 16); otherwise the
// hardcoded fallback is used.

const MIN_PASSWORD_LENGTH = 6;
const MIN_TOKEN_LENGTH = 16;

const DEFAULT_VERIFIED_EMAIL = "e2e-verified@hive-ci.test";
const DEFAULT_UNVERIFIED_EMAIL = "e2e-unverified@hive-ci.test";
const DEFAULT_VERIFIED_PASSWORD = "E2eFixture-Verified#2026";
const DEFAULT_UNVERIFIED_PASSWORD = "E2eFixture-Unverified#2026";
const DEFAULT_INVITATION_TOKEN = "e2e-invitation-token-2026-fixture";

function envOrDefault(
  name: string,
  fallback: string,
  minLength = 0
): string {
  const raw = process.env[name];
  if (!raw) {
    return fallback;
  }
  if (minLength > 0 && raw.length < minLength) {
    return fallback;
  }
  return raw;
}

export const E2E_VERIFIED_EMAIL = envOrDefault(
  "E2E_VERIFIED_EMAIL",
  DEFAULT_VERIFIED_EMAIL
);
export const E2E_UNVERIFIED_EMAIL = envOrDefault(
  "E2E_UNVERIFIED_EMAIL",
  DEFAULT_UNVERIFIED_EMAIL
);
export const E2E_VERIFIED_PASSWORD = envOrDefault(
  "E2E_VERIFIED_PASSWORD",
  DEFAULT_VERIFIED_PASSWORD,
  MIN_PASSWORD_LENGTH
);
export const E2E_UNVERIFIED_PASSWORD = envOrDefault(
  "E2E_UNVERIFIED_PASSWORD",
  DEFAULT_UNVERIFIED_PASSWORD,
  MIN_PASSWORD_LENGTH
);
export const E2E_INVITATION_TOKEN = envOrDefault(
  "E2E_INVITATION_TOKEN",
  DEFAULT_INVITATION_TOKEN,
  MIN_TOKEN_LENGTH
);
