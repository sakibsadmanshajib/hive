// @vitest-environment node
//
// Node, not the suite's default jsdom: the module under test is a Node-only
// seeder, and jsdom's AbortSignal carries neither `timeout` nor `any`.
import { afterEach, describe, expect, it, vi } from "vitest";
import { timedFetch } from "../e2e/support/e2e-fixture-seed.mjs";

// The fixture seeder used to hang for the full 120s that fixture-reset.ts
// allows a reseed, and then die to a bare kill signal with nothing on stderr.
// Six pull requests and one push to main burned 25 minutes each that way in
// August 2026 and left no evidence of which call had stalled. These are the
// guards on the deadline that replaced that silence.

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
  delete process.env.E2E_FIXTURE_CALL_TIMEOUT_MS;
  vi.restoreAllMocks();
});

describe("timedFetch", () => {
  it("names the endpoint that stalled, and drops the query string", async () => {
    process.env.E2E_FIXTURE_CALL_TIMEOUT_MS = "25";
    // A request that never settles until its signal aborts, which is exactly
    // what a stalled admin call looked like from Node's side.
    globalThis.fetch = ((_input: string, init: RequestInit = {}) =>
      new Promise((_resolve, reject) => {
        init.signal?.addEventListener("abort", () =>
          reject(new DOMException("aborted", "AbortError"))
        );
      })) as typeof globalThis.fetch;

    await expect(
      timedFetch(
        "https://project.supabase.co/rest/v1/account_profiles?login_email=like.%25%2B%25%40%25",
        { method: "PATCH" }
      )
    ).rejects.toThrow(
      /admin API call stalled: PATCH \/rest\/v1\/account_profiles did not answer within 25ms/
    );

    // The query string carries fixture email addresses, so it must not reach
    // a CI log that is public on this repository.
    let message = "";
    try {
      await timedFetch(
        "https://project.supabase.co/rest/v1/x?email=eq.someone@example.com"
      );
    } catch (err) {
      message = err instanceof Error ? err.message : String(err);
    }
    expect(message).toMatch(/admin API call stalled/);
    expect(message).not.toContain("example.com");
  });

  it("passes a caller-supplied signal through instead of discarding it", async () => {
    const seen: Array<AbortSignal | null | undefined> = [];
    globalThis.fetch = ((_input: string, init: RequestInit = {}) => {
      seen.push(init.signal);
      return Promise.resolve(new Response("{}", { status: 200 }));
    }) as typeof globalThis.fetch;

    const caller = new AbortController();
    await timedFetch("https://project.supabase.co/auth/v1/admin/users", {
      signal: caller.signal,
    });

    expect(seen).toHaveLength(1);
    expect(seen[0]).toBeInstanceOf(AbortSignal);
    // Composed, so aborting the caller's controller still aborts the request.
    caller.abort();
    expect(seen[0]?.aborted).toBe(true);
  });

  it("lets a real response through untouched", async () => {
    globalThis.fetch = (() =>
      Promise.resolve(new Response("ok", { status: 201 }))) as typeof globalThis.fetch;

    const res = await timedFetch("https://project.supabase.co/rest/v1/accounts");
    expect(res.status).toBe(201);
  });
});
