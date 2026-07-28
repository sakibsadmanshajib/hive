import { describe, expect, it } from "vitest";
import {
  buildIds,
  deriveUuid,
  redactSecrets,
  withRunKey,
} from "../e2e/support/e2e-fixture-seed.mjs";

// The E2E fixture seeder derives every id and email it writes from a run key,
// so two concurrent CI jobs never touch the same rows. That derivation used to
// live in a deployed Supabase Edge Function where nothing in this repo could
// check it. It runs in-process now, so these are the guards on it.

describe("fixture id derivation", () => {
  it("returns the shared local identity when no run key is given", () => {
    const ids = buildIds("");
    expect(ids.slugSuffix).toBe("");
    expect(ids.inviterEmail).toBe("e2e-inviter@scubed.com.bd");
    // Fixed uuid so repeated local runs stay idempotent against one row set.
    expect(ids.verifiedPrimaryAccountId).toBe(
      "31aadd76-fba0-46e6-827d-e3cfef50324c"
    );
  });

  it("is deterministic for a given run key", () => {
    expect(buildIds("run-1-attempt-1")).toEqual(buildIds("run-1-attempt-1"));
  });

  it("gives two run keys entirely disjoint ids", () => {
    const a = buildIds("run-1-attempt-1");
    const b = buildIds("run-1-attempt-2");
    const overlap = Object.keys(a).filter(
      (key) => a[key as keyof typeof a] === b[key as keyof typeof b]
    );
    expect(overlap).toEqual([]);
  });

  it("derives uuid-shaped values Postgres will accept", () => {
    expect(deriveUuid("anything")).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
    );
  });

  it("tags the email local part so a run gets its own auth user", () => {
    expect(withRunKey("e2e-inviter@scubed.com.bd", "42-1")).toBe(
      "e2e-inviter+42-1@scubed.com.bd"
    );
  });
});

describe("redactSecrets", () => {
  it("scrubs the service-role key out of text bound for the CI log", () => {
    const key = "sb-service-role-key-placeholder";
    expect(redactSecrets(`createUser failed: bad key ${key}`, [key])).toBe(
      "createUser failed: bad key <redacted>"
    );
  });

  it("leaves text alone when no secret is configured", () => {
    expect(redactSecrets("plain failure", [undefined])).toBe("plain failure");
  });
});
