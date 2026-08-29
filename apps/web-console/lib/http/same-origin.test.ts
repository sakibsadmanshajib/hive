import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { isSameOrigin, refuseCrossOrigin } from "./same-origin";

const CANONICAL = "https://console.example.test";

function requestWith(headers: Record<string, string>): { headers: Headers } {
  return { headers: new Headers(headers) };
}

describe("lib/http/same-origin", () => {
  beforeEach(() => {
    // A real (non-loopback) NEXT_PUBLIC_APP_URL outranks every header, so the
    // canonical side of the comparison is fixed for the whole suite.
    vi.stubEnv("NEXT_PUBLIC_APP_URL", CANONICAL);
  });

  afterEach(() => {
    vi.unstubAllEnvs();
  });

  describe("isSameOrigin", () => {
    it("allows an Origin that matches the canonical app origin", () => {
      expect(isSameOrigin(requestWith({ origin: CANONICAL }))).toBe(true);
    });

    it("allows a matching Origin that carries a trailing slash", () => {
      expect(isSameOrigin(requestWith({ origin: `${CANONICAL}/` }))).toBe(true);
    });

    it("refuses another host", () => {
      expect(isSameOrigin(requestWith({ origin: "https://evil.example" }))).toBe(false);
    });

    it("refuses a host that merely starts with the canonical one", () => {
      expect(
        isSameOrigin(requestWith({ origin: "https://console.example.test.evil.example" })),
      ).toBe(false);
    });

    it("refuses a scheme mismatch on the same host", () => {
      expect(isSameOrigin(requestWith({ origin: "http://console.example.test" }))).toBe(
        false,
      );
    });

    it("refuses a port mismatch on the same host", () => {
      expect(isSameOrigin(requestWith({ origin: `${CANONICAL}:8443` }))).toBe(false);
    });

    // The deliberate allowance, pinned so it cannot be changed silently in
    // either direction. See the comment in same-origin.ts for why.
    it("allows an absent Origin header", () => {
      expect(isSameOrigin(requestWith({}))).toBe(true);
    });

    // Absent and present-but-wrong are different cases. A permissive default
    // that collapsed these into the absent case would make the guard
    // decorative, so each present-but-unusable shape is pinned separately.
    it("refuses an empty Origin, which is present rather than absent", () => {
      expect(isSameOrigin(requestWith({ origin: "" }))).toBe(false);
    });

    it("refuses the literal null Origin a sandboxed frame sends", () => {
      expect(isSameOrigin(requestWith({ origin: "null" }))).toBe(false);
    });

    it("refuses an unparsable Origin", () => {
      expect(isSameOrigin(requestWith({ origin: "not a url" }))).toBe(false);
    });

    it("refuses an opaque-origin scheme", () => {
      expect(isSameOrigin(requestWith({ origin: "data:text/html,x" }))).toBe(false);
    });

    // The canonical origin is header-derived when no NEXT_PUBLIC_APP_URL names a
    // real host, which is how apps that bake no app URL resolve it. The guard
    // must still compare correctly there.
    it("compares against the forwarded host when no app URL is configured", () => {
      vi.stubEnv("NEXT_PUBLIC_APP_URL", "");
      expect(
        isSameOrigin(
          requestWith({ origin: "https://forwarded.example", host: "forwarded.example" }),
        ),
      ).toBe(true);
      expect(
        isSameOrigin(
          requestWith({ origin: "https://evil.example", host: "forwarded.example" }),
        ),
      ).toBe(false);
    });
  });

  describe("refuseCrossOrigin", () => {
    it("returns null for a same-origin request", () => {
      expect(refuseCrossOrigin(requestWith({ origin: CANONICAL }))).toBeNull();
    });

    it("returns null for an absent Origin", () => {
      expect(refuseCrossOrigin(requestWith({}))).toBeNull();
    });

    it("returns a 403 carrying a generic body for a cross-origin request", async () => {
      const res = refuseCrossOrigin(requestWith({ origin: "https://evil.example" }));
      expect(res).not.toBeNull();
      expect(res?.status).toBe(403);
      const body: unknown = await res?.json();
      expect(body).toEqual({ error: "Forbidden" });
    });
  });
});
