import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  isSameOrigin,
  refuseCrossOrigin,
  refuseCrossOriginMutation,
} from "./same-origin";

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

    // new URL("blob:https://host/x").origin is the INNER url's origin, so
    // without the scheme check this would compare equal to the canonical one.
    it("refuses a blob URL wrapping the canonical origin", () => {
      expect(isSameOrigin(requestWith({ origin: `blob:${CANONICAL}/1` }))).toBe(false);
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

    // The availability half, and the reason the request's own host is accepted
    // as well as the configured origin. NEXT_PUBLIC_APP_URL is baked into the
    // console image at build time while the hostname a browser reaches comes
    // from CONSOLE_DOMAIN and CONSOLE_EXTERNAL_SCHEME in Caddy, and
    // `.env.example` ships those divergent. Without this, a stock
    // `cp .env.example .env` deployment would 403 every mutation for a
    // legitimately signed-in operator.
    it("accepts the host the request was actually addressed to, even when the configured origin differs", () => {
      vi.stubEnv("NEXT_PUBLIC_APP_URL", "https://console-hive.scubed.co");
      expect(
        isSameOrigin(
          requestWith({
            origin: "http://console.localhost",
            host: "console.localhost",
            "x-forwarded-proto": "http",
          }),
        ),
      ).toBe(true);
    });

    // Accepting the request's own host is not a hole: an attacker has to make
    // Origin and Host agree, and a browser sets Host from the URL that scopes
    // the session cookie, so a forged post carries the victim's host and the
    // attacker's origin.
    it("still refuses a foreign Origin arriving at the deployment's own host", () => {
      vi.stubEnv("NEXT_PUBLIC_APP_URL", "https://console-hive.scubed.co");
      expect(
        isSameOrigin(
          requestWith({
            origin: "https://artifacts-hive.scubed.co",
            host: "console-hive.scubed.co",
          }),
        ),
      ).toBe(false);
    });
  });

  describe("refuseCrossOriginMutation", () => {
    function mutation(
      method: string,
      pathname: string,
      headers: Record<string, string>,
    ): { method: string; headers: Headers; nextUrl: { pathname: string } } {
      return { method, headers: new Headers(headers), nextUrl: { pathname } };
    }

    it("refuses a cross-origin POST", () => {
      const res = refuseCrossOriginMutation(
        mutation("POST", "/api/console/members", { origin: "https://evil.example" }),
      );
      expect(res?.status).toBe(403);
    });

    it("refuses a cross-origin PUT, PATCH and DELETE", () => {
      for (const method of ["PUT", "PATCH", "DELETE"]) {
        const res = refuseCrossOriginMutation(
          mutation(method, "/api/budget", { origin: "https://evil.example" }),
        );
        expect(res?.status).toBe(403);
      }
    });

    // A cross-origin GET is how every link into the console works.
    it("lets safe methods through regardless of Origin", () => {
      for (const method of ["GET", "HEAD", "OPTIONS", "get"]) {
        expect(
          refuseCrossOriginMutation(
            mutation(method, "/console", { origin: "https://evil.example" }),
          ),
        ).toBeNull();
      }
    });

    it("lets the SSLCommerz payment return through, which is a real cross-origin POST", () => {
      expect(
        refuseCrossOriginMutation(
          mutation("POST", "/api/payments/return/sslcommerz", {
            origin: "https://sandbox.sslcommerz.com",
          }),
        ),
      ).toBeNull();
    });

    it("does not extend that exemption to a neighbouring path", () => {
      const res = refuseCrossOriginMutation(
        mutation("POST", "/api/payments/return/sslcommerz/steal", {
          origin: "https://sandbox.sslcommerz.com",
        }),
      );
      expect(res?.status).toBe(403);
    });

    it("accepts a same-origin POST", () => {
      expect(
        refuseCrossOriginMutation(
          mutation("POST", "/api/console/members", { origin: CANONICAL }),
        ),
      ).toBeNull();
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
