import { describe, expect, it } from "vitest";
import {
  SHARED_DEMO_ACCOUNT,
  assertNotSharedDemoAccount,
} from "../e2e/support/live-auth.mjs";

// docs/live-test-auth.md has always said a write-capable suite must never
// authenticate as the shared demo account, and until this guard nothing
// enforced it: that account collected 24 conversations of automation text,
// five of them on the day the guard was written (issues #848, #916).
//
// These tests are the enforcement. Deleting the guard, or letting the address
// comparison drift (a stray capital, a trailing space), turns them red.

describe("assertNotSharedDemoAccount", () => {
  it("refuses the shared demo account by default", () => {
    expect(() => assertNotSharedDemoAccount(SHARED_DEMO_ACCOUNT)).toThrow(
      /refusing to mint a session/i
    );
  });

  it("names the account and the document in the failure", () => {
    let message = "";
    try {
      assertNotSharedDemoAccount(SHARED_DEMO_ACCOUNT);
    } catch (error) {
      message = error instanceof Error ? error.message : String(error);
    }
    expect(message).toContain(SHARED_DEMO_ACCOUNT);
    expect(message).toContain("docs/live-test-auth.md");
    expect(message).toContain("E2E_RUN_KEY");
  });

  it("refuses regardless of case or surrounding whitespace", () => {
    expect(() =>
      assertNotSharedDemoAccount(`  ${SHARED_DEMO_ACCOUNT.toUpperCase()} `)
    ).toThrow(/refusing to mint a session/i);
  });

  it("allows the demo account only when the run declares itself read only", () => {
    expect(() =>
      assertNotSharedDemoAccount(SHARED_DEMO_ACCOUNT, { readOnly: true })
    ).not.toThrow();
  });

  it("allows a run-scoped fixture identity", () => {
    expect(() =>
      assertNotSharedDemoAccount("e2e-verified+run-key-123@hive-e2e.invalid")
    ).not.toThrow();
  });

  it("allows a different account on the same domain", () => {
    // Only the one shared address is reserved. owner@ and showcase@ are
    // separate accounts and are not what the rule protects.
    expect(() => assertNotSharedDemoAccount("owner@hive-demo.invalid")).not.toThrow();
  });

  it("does not throw on a missing address, which mintSession reports itself", () => {
    expect(() => assertNotSharedDemoAccount(undefined)).not.toThrow();
  });

  it("cannot be switched off by an environment variable", () => {
    // The read-only declaration is per call on purpose. An env var belongs to
    // whoever set it, so one line in a workflow's env: block would disable the
    // guard for every step in that job. If someone reintroduces an env-var
    // escape hatch, this goes red.
    const previous = process.env.HIVE_LIVE_AUTH_READ_ONLY;
    process.env.HIVE_LIVE_AUTH_READ_ONLY = "1";
    try {
      expect(() => assertNotSharedDemoAccount(SHARED_DEMO_ACCOUNT)).toThrow(
        /refusing to mint a session/i
      );
    } finally {
      if (previous === undefined) {
        delete process.env.HIVE_LIVE_AUTH_READ_ONLY;
      } else {
        process.env.HIVE_LIVE_AUTH_READ_ONLY = previous;
      }
    }
  });
});
