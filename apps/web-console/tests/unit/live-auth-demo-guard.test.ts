import { afterEach, describe, expect, it, vi } from "vitest";
import {
  SHARED_DEMO_ACCOUNT,
  assertNotSharedDemoAccount,
  mintSession,
  writeStorageState,
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

// The block above tests the helper. A perfect helper that nothing calls is the
// exact defect this repository keeps shipping, so these test the wiring: that
// `mintSession` reaches the guard, and that it does so before it opens a socket.
// Deleting the `assertNotSharedDemoAccount(email, { readOnly })` line from
// `mintSession` turns both of these red; deleting it turns none of the helper
// tests above red.
describe("mintSession refuses the shared demo account at the choke point", () => {
  const SUPABASE_ENV = {
    SUPABASE_URL: "https://supabase.invalid",
    SUPABASE_SERVICE_ROLE_KEY: "service-role-not-a-real-key",
    SUPABASE_ANON_KEY: "anon-not-a-real-key",
  } as const;

  afterEach(() => {
    vi.unstubAllEnvs();
    vi.unstubAllGlobals();
  });

  /** Env set so the mint gets past requiredEnv, and fetch stubbed so a network
   * call is observable rather than merely slow. A `fetch` that throws would
   * also surface as a rejection, which is why the assertion is on the message
   * and on the call count, not just on "it rejected". */
  function armFetchTrap() {
    for (const [name, value] of Object.entries(SUPABASE_ENV)) {
      vi.stubEnv(name, value);
    }
    const fetchSpy = vi.fn(() => {
      throw new Error("live-auth test: the guard let a network call through");
    });
    vi.stubGlobal("fetch", fetchSpy);
    return fetchSpy;
  }

  it("rejects, and opens no socket, before generate_link", async () => {
    const fetchSpy = armFetchTrap();
    await expect(mintSession({ email: SHARED_DEMO_ACCOUNT })).rejects.toThrow(
      /refusing to mint a session/i
    );
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("rejects the same address written with a stray capital and space", async () => {
    const fetchSpy = armFetchTrap();
    await expect(
      mintSession({ email: ` ${SHARED_DEMO_ACCOUNT.toUpperCase()} ` })
    ).rejects.toThrow(/refusing to mint a session/i);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("lets the demo account through when the run declares itself read only", async () => {
    const fetchSpy = armFetchTrap();
    // Past the guard, so the stub fires and the failure is the network one.
    // That is the point: the readOnly escape has to actually escape, or the
    // rejection above would prove nothing about which check refused.
    await expect(
      mintSession({ email: SHARED_DEMO_ACCOUNT, readOnly: true })
    ).rejects.toThrow(/the guard let a network call through/);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
  });

  it("guards writeStorageState too, which reaches it only through mintSession", async () => {
    const fetchSpy = armFetchTrap();
    await expect(
      writeStorageState({
        email: SHARED_DEMO_ACCOUNT,
        targetUrl: "https://chat.invalid/",
        statePath: "/dev/null/never-written.json",
      })
    ).rejects.toThrow(/refusing to mint a session/i);
    // reauthenticate() shares that single call to mintSession, so it is covered
    // by the same line; there is no second door to test.
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});
