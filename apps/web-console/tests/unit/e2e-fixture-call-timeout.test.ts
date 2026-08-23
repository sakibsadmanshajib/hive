// @vitest-environment node
//
// Node, not the suite's default jsdom: the module under test is a Node-only
// seeder, and jsdom's AbortSignal carries neither `timeout` nor `any`.
import { afterEach, describe, expect, it, vi } from "vitest";
import { timedFetch } from "../e2e/support/e2e-fixture-seed.mjs";

// Without a per-call deadline the fixture seeder hangs for the full 120s that
// fixture-reset.ts allows a reseed and then dies to a bare kill signal with
// nothing on stderr, so the run says only that seeding took too long, never
// which call was stuck. These are the guards on the deadline that replaced
// that silence.

const originalFetch = globalThis.fetch;

/** A request that never settles until its own signal aborts. */
function neverSettles() {
  return ((_input: unknown, init: RequestInit = {}) =>
    new Promise((_resolve, reject) => {
      init.signal?.addEventListener("abort", () =>
        reject(new DOMException("aborted", "AbortError"))
      );
    })) as typeof globalThis.fetch;
}

afterEach(() => {
  globalThis.fetch = originalFetch;
  delete process.env.E2E_FIXTURE_CALL_TIMEOUT_MS;
  vi.restoreAllMocks();
});

describe("timedFetch", () => {
  it("names the endpoint that stalled, and drops the query string", async () => {
    process.env.E2E_FIXTURE_CALL_TIMEOUT_MS = "25";
    globalThis.fetch = neverSettles();

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

  it("labels a Request input with its own method, and accepts a URL input", async () => {
    process.env.E2E_FIXTURE_CALL_TIMEOUT_MS = "25";
    globalThis.fetch = neverSettles();

    // fetch takes a string, a URL or a Request. Reading `.url` off a URL
    // yields undefined and `new URL(undefined)` throws, which would replace
    // the stall message with a TypeError from the diagnostic itself.
    await expect(
      timedFetch(
        new Request("https://project.supabase.co/auth/v1/admin/users", {
          method: "PATCH",
        })
      )
    ).rejects.toThrow(
      /admin API call stalled: PATCH \/auth\/v1\/admin\/users did not answer/
    );

    await expect(
      timedFetch(new URL("https://project.supabase.co/rest/v1/accounts"))
    ).rejects.toThrow(
      /admin API call stalled: GET \/rest\/v1\/accounts did not answer/
    );
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
      Promise.resolve(
        new Response("ok", { status: 201 })
      )) as typeof globalThis.fetch;

    const res = await timedFetch("https://project.supabase.co/rest/v1/accounts");
    expect(res.status).toBe(201);
  });
});
