import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it, vi } from "vitest";

// This repository is public. Its E2E fixture file used to carry two live
// account passwords and a live invitation token in plaintext, and the seeder
// wrote those exact values back onto the accounts on every credential-less
// run, so they were real credentials rather than inert sample data. These
// guards exist so that cannot come back quietly.

const DEFAULTS_PATH = join(
  __dirname,
  "..",
  "e2e",
  "support",
  "e2e-auth-defaults.json"
);

describe("e2e-auth-defaults.json", () => {
  it("carries addresses and limits only, never a credential", () => {
    const parsed = JSON.parse(readFileSync(DEFAULTS_PATH, "utf8"));
    expect(Object.keys(parsed).sort()).toEqual([
      "minPasswordLength",
      "minTokenLength",
      "unverifiedEmail",
      "verifiedEmail",
    ]);
  });

  // Belt to the braces above: a future field named something other than
  // "verifiedPassword" would pass the key check, so assert on shape too. Every
  // legitimate value here is an address or a small integer.
  it("holds no free-form secret-shaped string", () => {
    const parsed = JSON.parse(readFileSync(DEFAULTS_PATH, "utf8"));
    for (const [key, value] of Object.entries(parsed)) {
      if (typeof value === "string") {
        expect(value, `${key} must be an email address, not a secret`).toMatch(
          /^[^@\s]+@[^@\s]+\.[^@\s]+$/
        );
      } else {
        expect(typeof value, `${key} must be a number`).toBe("number");
      }
    }
  });
});

describe("requiredSecretEnv", () => {
  it("names the missing variable instead of falling back or skipping", async () => {
    // Set the three the module resolves at import time, so importing it here
    // exercises the helper rather than the module's own startup failure.
    vi.stubEnv("E2E_VERIFIED_PASSWORD", "set-for-import-only");
    vi.stubEnv("E2E_UNVERIFIED_PASSWORD", "set-for-import-only");
    vi.stubEnv("E2E_INVITATION_TOKEN", "set-for-import-only-token");
    vi.stubEnv("E2E_RUN_KEY", "unit-test");
    const { requiredSecretEnv } = await import(
      "../e2e/support/e2e-auth-creds"
    );

    expect(() => requiredSecretEnv("E2E_ABSENT_FIXTURE_VAR", 1)).toThrow(
      /E2E_ABSENT_FIXTURE_VAR is required and has no default/
    );

    vi.stubEnv("E2E_PRESENT_FIXTURE_VAR", "");
    expect(() => requiredSecretEnv("E2E_PRESENT_FIXTURE_VAR", 1)).toThrow(
      /E2E_PRESENT_FIXTURE_VAR is required and has no default/
    );

    vi.stubEnv("E2E_PRESENT_FIXTURE_VAR", "short");
    expect(() => requiredSecretEnv("E2E_PRESENT_FIXTURE_VAR", 12)).toThrow(
      /too short/
    );

    vi.stubEnv("E2E_PRESENT_FIXTURE_VAR", "long-enough-value");
    expect(requiredSecretEnv("E2E_PRESENT_FIXTURE_VAR", 12)).toBe(
      "long-enough-value"
    );

    vi.unstubAllEnvs();
  });
});

// Seeding writes a password to whatever address it is given. Before this
// guard, a run with no run key targeted the two shared live tenant-OWNER
// accounts, so every local run revoked the sessions of every other run.
describe("runScopedEmail", () => {
  it("refuses to resolve an address without a run key, and namespaces once", async () => {
    vi.stubEnv("E2E_VERIFIED_PASSWORD", "set-for-import-only");
    vi.stubEnv("E2E_UNVERIFIED_PASSWORD", "set-for-import-only");
    vi.stubEnv("E2E_INVITATION_TOKEN", "set-for-import-only-token");
    vi.stubEnv("E2E_RUN_KEY", "unit-test");
    const { runScopedEmail } = await import("../e2e/support/e2e-auth-creds");

    expect(() => runScopedEmail("e2e-verified@scubed.com.bd", "")).toThrow(
      /E2E_RUN_KEY is required and has no default/
    );

    // A shared base address becomes this run's own address.
    expect(runScopedEmail("e2e-verified@scubed.com.bd", "99-1")).toBe(
      "e2e-verified+99-1@scubed.com.bd"
    );

    // Idempotent: CI already passes a namespaced address alongside the same
    // key, and doubling the suffix would seed an account no spec signs in as.
    expect(runScopedEmail("e2e-verified+99-1@scubed.com.bd", "99-1")).toBe(
      "e2e-verified+99-1@scubed.com.bd"
    );

    // Two runs never resolve to the same account.
    expect(runScopedEmail("e2e-verified@scubed.com.bd", "99-1")).not.toBe(
      runScopedEmail("e2e-verified@scubed.com.bd", "99-2")
    );

    vi.unstubAllEnvs();
  });
});
