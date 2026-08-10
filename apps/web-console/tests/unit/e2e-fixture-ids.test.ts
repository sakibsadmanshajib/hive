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

// The shape GoTrue actually returns from GET /auth/v1/verify: the session is
// handed back in the URL FRAGMENT, never in the query string. A real value,
// but from a throwaway key generated for this test, not from any account.
const LIVE_TOKEN =
  "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
  "eyJzdWIiOiJkZW1vIiwicm9sZSI6ImF1dGhlbnRpY2F0ZWQifQ." +
  "3sHq0ZP9Lc1nJmVxV0dQeR7yUu2bWaKcXtNfGhIjLmO";
const VERIFY_REDIRECT =
  `https://chat-hive.scubed.co/agent-workspace/tasks#access_token=${LIVE_TOKEN}` +
  "&expires_in=3600&refresh_token=xk4t2vqz9m1p&token_type=bearer";

// The redactor that leaked on 2026-08-08, kept here as the failing baseline.
// It is a faithful reproduction, not a strawman: split the URL on "?", scrub
// the query parameters, print the rest. Every assertion below that this one
// fails is the evidence that the shipped redactor is doing something the
// obvious implementation does not.
function queryStringOnlyRedactor(text: string): string {
  return text.replace(/\?[^\s]*/g, (queryPart) =>
    queryPart.replace(
      /\b(access_token|refresh_token|token|code|apikey)=[^&\s]+/gi,
      (_m, name: string) => `${name}=<redacted>`
    )
  );
}

describe("redactSecrets: URL-borne credentials", () => {
  it("the query-string-only redactor leaks a fragment token (the 2026-08-08 bug)", () => {
    // RED first: this is the exact failure the guard below exists to prevent.
    expect(queryStringOnlyRedactor(VERIFY_REDIRECT)).toContain(LIVE_TOKEN);
  });

  it("scrubs a session token carried in a URL fragment", () => {
    const redacted = redactSecrets(VERIFY_REDIRECT, [undefined]);
    expect(redacted).not.toContain(LIVE_TOKEN);
    expect(redacted).toContain("access_token=<redacted>");
    expect(redacted).toContain("refresh_token=<redacted>");
    // The non-secret parts of the URL survive, so a log line is still useful
    // for diagnosis.
    expect(redacted).toContain("https://chat-hive.scubed.co/agent-workspace/tasks");
    expect(redacted).toContain("expires_in=3600");
  });

  it("scrubs a one-time OTP carried in a query string", () => {
    const actionLink =
      "https://xyz.supabase.co/auth/v1/verify?token=pkce_9f3c1&type=magiclink&redirect_to=https://chat-hive.scubed.co/";
    const redacted = redactSecrets(actionLink, [undefined]);
    expect(redacted).not.toContain("pkce_9f3c1");
    expect(redacted).toContain("token=<redacted>");
    expect(redacted).toContain("type=magiclink");
  });

  it("scrubs a bare JWT with no parameter name around it", () => {
    expect(redactSecrets(`Authorization: Bearer ${LIVE_TOKEN}`, [undefined])).toBe(
      "Authorization: Bearer <redacted>"
    );
  });

  it("does not mistake a longer credential name for a shorter one", () => {
    const redacted = redactSecrets(
      "#provider_refresh_token=abc123&hashed_token=def456",
      [undefined]
    );
    expect(redacted).toBe(
      "#provider_refresh_token=<redacted>&hashed_token=<redacted>"
    );
  });
});
