import { afterEach, describe, expect, it, vi } from "vitest";
import {
  MIN_RUN_KEY_LENGTH,
  PROTECTED_ACCOUNT_BASES,
  assertNotSharedDemoAccount,
  mintSession,
  writeStorageState,
} from "../e2e/support/live-auth.mjs";

// docs/live-test-auth.md has always said a write-capable suite must never
// authenticate as the shared demo account, and until this guard nothing
// enforced it: that account collected 24 conversations of automation text,
// five of them on the day the guard was written (issues #848, #916). Issue
// #1476 found three more shared accounts with the same accumulation cause and
// flipped the guard from a denylist of that one address to an allowlist: a
// protected base is refused unless its address carries an exact `+tag` equal
// to this run's `E2E_RUN_KEY` (at least MIN_RUN_KEY_LENGTH characters), or
// the caller declares the run read only.
//
// An earlier version of this file compared the run key against the *whole*
// address as a substring, domain included, which a security review found lets
// a short, common key open a protected base with its address completely
// unchanged: `E2E_RUN_KEY=-` opened all three bases; `hive` opened two;
// `qa`/`test` opened `qa-tester@hive.test`; `e2e`/`2`/`bd` opened
// `e2e-verified@scubed.com.bd`; `invalid`/`demo` opened
// `demo@hive-demo.invalid`. The "bypass table" describe block below pins
// every one of those keys, against every base, refused with no tag present.
//
// These tests are the enforcement. Deleting the guard, or letting the base
// comparison drift (a stray capital, a trailing space, a `+tag` that should
// not hide the base underneath it, a substring match standing in for an
// exact one), turns them red.

const DEMO_ACCOUNT = PROTECTED_ACCOUNT_BASES[0];

describe("assertNotSharedDemoAccount", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it.each(PROTECTED_ACCOUNT_BASES)("refuses %s by default", (base) => {
    expect(() => assertNotSharedDemoAccount(base)).toThrow(/refusing to mint a session/i);
  });

  it("names the account and the document in the failure", () => {
    let message = "";
    try {
      assertNotSharedDemoAccount(DEMO_ACCOUNT);
    } catch (error) {
      message = error instanceof Error ? error.message : String(error);
    }
    expect(message).toContain(DEMO_ACCOUNT);
    expect(message).toContain("docs/live-test-auth.md");
    expect(message).toContain("E2E_RUN_KEY");
    expect(message).toContain("#1476");
  });

  it("refuses regardless of case or surrounding whitespace", () => {
    expect(() =>
      assertNotSharedDemoAccount(`  ${DEMO_ACCOUNT.toUpperCase()} `)
    ).toThrow(/refusing to mint a session/i);
  });

  it("refuses a leading U+FEFF the same as any other edge character", () => {
    // JS's String.prototype.trim() already folds this in; the guard's own
    // normaliser must not be looser than that in either direction, so this is
    // pinned explicitly rather than left to trim()'s behaviour to change
    // under it unnoticed.
    expect(() => assertNotSharedDemoAccount(`﻿${DEMO_ACCOUNT}`)).toThrow(
      /refusing to mint a session/i
    );
  });

  it("refuses a trailing U+001C the same as any other edge character", () => {
    // Python's str.strip() folds this in; JS's trim() does not on its own,
    // so shared_demo_account.py's guard must agree with this one explicitly
    // or the same address is refused by one entry point and allowed by the
    // other.
    expect(() =>
      assertNotSharedDemoAccount(`qa-tester@hive.test\x1c`)
    ).toThrow(/refusing to mint a session/i);
  });

  it("allows a protected base only when the run declares itself read only", () => {
    expect(() =>
      assertNotSharedDemoAccount(DEMO_ACCOUNT, { readOnly: true })
    ).not.toThrow();
  });

  it("allows a protected base scoped to this run's E2E_RUN_KEY", () => {
    vi.stubEnv("E2E_RUN_KEY", "run-4242");
    expect(() =>
      assertNotSharedDemoAccount("e2e-verified+run-4242@scubed.com.bd")
    ).not.toThrow();
  });

  it("still refuses a different run's tag on the same base", () => {
    vi.stubEnv("E2E_RUN_KEY", "run-4242");
    expect(() =>
      assertNotSharedDemoAccount("e2e-verified+run-9999@scubed.com.bd")
    ).toThrow(/refusing to mint a session/i);
  });

  it("does not let an empty E2E_RUN_KEY allow a protected base through", () => {
    vi.stubEnv("E2E_RUN_KEY", "");
    expect(() => assertNotSharedDemoAccount(DEMO_ACCOUNT)).toThrow(
      /refusing to mint a session/i
    );
  });

  it("refuses the bare base even when a run key is set (no tag at all)", () => {
    vi.stubEnv("E2E_RUN_KEY", "run-4242");
    expect(() =>
      assertNotSharedDemoAccount("e2e-verified@scubed.com.bd")
    ).toThrow(/refusing to mint a session/i);
  });

  it("refuses a run key shorter than MIN_RUN_KEY_LENGTH even as an exact tag match", () => {
    const shortKey = "x".repeat(MIN_RUN_KEY_LENGTH - 1);
    vi.stubEnv("E2E_RUN_KEY", shortKey);
    expect(() =>
      assertNotSharedDemoAccount(`e2e-verified+${shortKey}@scubed.com.bd`)
    ).toThrow(/refusing to mint a session/i);
  });

  it("refuses a run key that only resembles the domain, not the local-part tag", () => {
    // The tag is "notrun", not the run key; "scubedotcombd" only resembles
    // the domain "scubed.com.bd". Exact-tag comparison must still refuse
    // this, proving the domain never participates in the match.
    vi.stubEnv("E2E_RUN_KEY", "scubedotcombd");
    expect(() =>
      assertNotSharedDemoAccount("e2e-verified+notrun@scubed.com.bd")
    ).toThrow(/refusing to mint a session/i);
  });

  describe("bypass table (substring-of-whole-address regression, not just the tag)", () => {
    // Each key below is a substring of the bare address for at least one
    // protected base. A substring-of-the-whole-address check (the bug this
    // guards against) would let the matching base(s) through with the
    // address completely unchanged, no +tag at all.
    const bypassKeys = ["-", "hive", "qa", "test", "e2e", "2", "bd", "invalid", "demo"];

    it.each(bypassKeys.flatMap((key) => PROTECTED_ACCOUNT_BASES.map((base) => [key, base])))(
      "E2E_RUN_KEY=%s does not open %s (bare address, no tag)",
      (key, base) => {
        vi.stubEnv("E2E_RUN_KEY", key);
        expect(() => assertNotSharedDemoAccount(base)).toThrow(/refusing to mint a session/i);
      }
    );
  });

  it("allows a different account on the same domain", () => {
    // Only the protected bases are reserved. owner@ is a separate account and
    // is not one of them, with or without a run key.
    expect(() => assertNotSharedDemoAccount("owner@hive-demo.invalid")).not.toThrow();
  });

  it("allows an unrelated dedicated address with no run key and no read_only", () => {
    // rag-verify-e2e@hive-e2e.invalid (verify-rag-roundtrip.py) is not a
    // protected base and never needed either escape.
    expect(() =>
      assertNotSharedDemoAccount("rag-verify-e2e@hive-e2e.invalid")
    ).not.toThrow();
  });

  it("does not throw on a missing address, which mintSession reports itself", () => {
    expect(() => assertNotSharedDemoAccount(undefined)).not.toThrow();
  });

  it("cannot be switched off by an unrelated environment variable", () => {
    // The read-only declaration is per call on purpose. An env var belongs to
    // whoever set it, so one line in a workflow's env: block would disable the
    // guard for every step in that job. If someone reintroduces an env-var
    // escape hatch, this goes red.
    const previous = process.env.HIVE_LIVE_AUTH_READ_ONLY;
    process.env.HIVE_LIVE_AUTH_READ_ONLY = "1";
    try {
      expect(() => assertNotSharedDemoAccount(DEMO_ACCOUNT)).toThrow(
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
describe("mintSession refuses a protected base at the choke point", () => {
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
    await expect(mintSession({ email: DEMO_ACCOUNT })).rejects.toThrow(
      /refusing to mint a session/i
    );
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("rejects the same address written with a stray capital and space", async () => {
    const fetchSpy = armFetchTrap();
    await expect(
      mintSession({ email: ` ${DEMO_ACCOUNT.toUpperCase()} ` })
    ).rejects.toThrow(/refusing to mint a session/i);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("lets a protected base through when the run declares itself read only", async () => {
    const fetchSpy = armFetchTrap();
    // Past the guard, so the stub fires and the failure is the network one.
    // That is the point: the readOnly escape has to actually escape, or the
    // rejection above would prove nothing about which check refused.
    await expect(
      mintSession({ email: DEMO_ACCOUNT, readOnly: true })
    ).rejects.toThrow(/the guard let a network call through/);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
  });

  it("lets a protected base through when it is scoped to E2E_RUN_KEY", async () => {
    const fetchSpy = armFetchTrap();
    vi.stubEnv("E2E_RUN_KEY", "run-4242");
    await expect(
      mintSession({ email: "e2e-verified+run-4242@scubed.com.bd" })
    ).rejects.toThrow(/the guard let a network call through/);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
  });

  it("guards writeStorageState too, which reaches it only through mintSession", async () => {
    const fetchSpy = armFetchTrap();
    await expect(
      writeStorageState({
        email: DEMO_ACCOUNT,
        targetUrl: "https://chat.invalid/",
        statePath: "/dev/null/never-written.json",
      })
    ).rejects.toThrow(/refusing to mint a session/i);
    // reauthenticate() shares that single call to mintSession, so it is covered
    // by the same line; there is no second door to test.
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});
