// @vitest-environment node
//
// Precedence contract for resolveCanonicalOrigin. The same matrix is asserted
// in apps/agent-console/lib/http/origin.test.ts against that app's copy of the
// helper, so the two cannot drift apart silently.
import { describe, it, expect, beforeEach, afterEach } from "vitest";

import { resolveCanonicalOrigin } from "./origin";

function requestWith(headers: Record<string, string>): { headers: Headers } {
  return { headers: new Headers(headers) };
}

const ORIGINAL_APP_URL = process.env.NEXT_PUBLIC_APP_URL;

describe("resolveCanonicalOrigin precedence", () => {
  beforeEach(() => {
    delete process.env.NEXT_PUBLIC_APP_URL;
  });

  afterEach(() => {
    if (ORIGINAL_APP_URL === undefined) {
      delete process.env.NEXT_PUBLIC_APP_URL;
    } else {
      process.env.NEXT_PUBLIC_APP_URL = ORIGINAL_APP_URL;
    }
  });

  it("prefers a non-loopback NEXT_PUBLIC_APP_URL over any request header", () => {
    // The #157 anti-open-redirect property: an operator-configured canonical
    // origin is unspoofable, so a forged X-Forwarded-Host must not win.
    process.env.NEXT_PUBLIC_APP_URL = "https://console-hive.scubed.co";

    expect(
      resolveCanonicalOrigin(
        requestWith({
          "x-forwarded-host": "evil.test",
          "x-forwarded-proto": "https",
        }),
      ),
    ).toBe("https://console-hive.scubed.co");
  });

  it("does not let a stale loopback NEXT_PUBLIC_APP_URL poison a deployed origin", () => {
    // .env.example ships NEXT_PUBLIC_APP_URL=http://localhost:3000. A
    // deployment that carries that value forward would otherwise 307 real
    // users to their own machine and mail localhost verification links.
    process.env.NEXT_PUBLIC_APP_URL = "http://localhost:3000";

    expect(
      resolveCanonicalOrigin(
        requestWith({
          "x-forwarded-host": "console-hive.scubed.co",
          "x-forwarded-proto": "https",
        }),
      ),
    ).toBe("https://console-hive.scubed.co");
  });

  it("keeps a loopback NEXT_PUBLIC_APP_URL when no forwarded host is present", () => {
    // Local development: nothing is in front of the server, so the configured
    // loopback origin is the right answer.
    process.env.NEXT_PUBLIC_APP_URL = "http://localhost:3000";

    expect(resolveCanonicalOrigin(requestWith({}))).toBe("http://localhost:3000");
  });

  it("falls back to the forwarded host when NEXT_PUBLIC_APP_URL is unset", () => {
    // apps/agent-console bakes its NEXT_PUBLIC_* as build args and deliberately
    // has no NEXT_PUBLIC_APP_URL, so this path has to carry that deployment.
    expect(
      resolveCanonicalOrigin(
        requestWith({
          "x-forwarded-host": "chat-hive.scubed.co",
          "x-forwarded-proto": "https",
        }),
      ),
    ).toBe("https://chat-hive.scubed.co");
  });

  it("uses the Host header when no X-Forwarded-Host is present", () => {
    expect(
      resolveCanonicalOrigin(
        requestWith({ host: "console.localhost", "x-forwarded-proto": "http" }),
      ),
    ).toBe("http://console.localhost");
  });

  it("never returns a wildcard bind address even when the Host header carries one", () => {
    // A direct container hit sends `Host: 0.0.0.0:3000`. That is the shape the
    // whole bug family is about, so it must never reach a Location header.
    const origin = resolveCanonicalOrigin(
      requestWith({ host: "0.0.0.0:3000" }),
    );

    expect(origin).not.toContain("0.0.0.0");
    expect(origin).toBe("http://localhost:3000");
  });

  it("rejects a wildcard NEXT_PUBLIC_APP_URL in favour of the forwarded host", () => {
    process.env.NEXT_PUBLIC_APP_URL = "http://0.0.0.0:3000";

    expect(
      resolveCanonicalOrigin(
        requestWith({
          "x-forwarded-host": "console-hive.scubed.co",
          "x-forwarded-proto": "https",
        }),
      ),
    ).toBe("https://console-hive.scubed.co");
  });

  it("ignores an unparseable forwarded host rather than splicing it into a Location", () => {
    expect(
      resolveCanonicalOrigin(
        requestWith({ "x-forwarded-host": "not a host/../evil" }),
      ),
    ).toBe("http://localhost:3000");
  });

  it("defaults a non-loopback forwarded host to https when no proto header is present", () => {
    expect(
      resolveCanonicalOrigin(requestWith({ "x-forwarded-host": "console-hive.scubed.co" })),
    ).toBe("https://console-hive.scubed.co");
  });

  it("defaults a loopback forwarded host to http when no proto header is present", () => {
    expect(
      resolveCanonicalOrigin(requestWith({ "x-forwarded-host": "localhost:3000" })),
    ).toBe("http://localhost:3000");
  });
});
