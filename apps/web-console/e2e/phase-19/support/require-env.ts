import { test } from "@playwright/test";

/**
 * Read an environment value a phase-19 spec cannot run without.
 *
 * Issue #659. Every spec in this directory used to open with
 * `if (!process.env.X) test.skip(...)`, and none of the X names appeared in
 * any workflow file. The result was a suite that reported green while
 * asserting nothing: a skipped test and a passing test are indistinguishable
 * inside a green check, so the specs that prove one tenant cannot read
 * another tenant's data had never executed once.
 *
 * Outside CI an unset value still skips, so the suite stays runnable against
 * a partially provisioned dev stack. Inside CI it throws. The workflow is
 * responsible for minting every one of these (see
 * scripts/seed-phase19-e2e.py), so a missing value there means the wiring
 * broke, and a broken wire should be a red run rather than a quiet one.
 *
 * `unavailableReason` is for the one value CI genuinely cannot produce. Pass
 * it and the CI-side error explains why instead of implying somebody forgot
 * a secret.
 */
export function requireEnv(name: string, unavailableReason?: string): string {
  const value = process.env[name];
  if (value) {
    return value;
  }
  if (process.env.CI) {
    const detail = unavailableReason
      ? `\n${unavailableReason}`
      : "\nThe workflow is expected to mint this (scripts/seed-phase19-e2e.py). " +
        "It did not, so this spec has no coverage.";
    throw new Error(
      `${name} is not set in CI; this spec must not silently skip (issue #659).${detail}`,
    );
  }
  test.skip(true, `${name} not set`);
  // Unreachable: test.skip throws. Present so callers can treat the result
  // as a plain string.
  return "";
}
