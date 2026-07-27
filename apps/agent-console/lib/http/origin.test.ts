// @vitest-environment node
//
// Precedence contract for this app's copy of resolveCanonicalOrigin. Mirrors
// apps/web-console/lib/http/origin.test.ts on purpose: the two apps are
// separate npm packages with separate build contexts, so the helper is
// duplicated and both copies need the same matrix pinned.
//
// The NEXT_PUBLIC_APP_URL-unset cases matter most here. This app bakes its
// NEXT_PUBLIC_* values as build args (deploy/docker/Dockerfile.agent-console)
// and deliberately has no NEXT_PUBLIC_APP_URL, so the forwarded-host fallback
// is the path that actually carries the deployment.
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

  it("falls back to the forwarded host when NEXT_PUBLIC_APP_URL is unset", () => {
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
        requestWith({ host: "chat.localhost", "x-forwarded-proto": "http" }),
      ),
    ).toBe("http://chat.localhost");
  });

  it("never returns a wildcard bind address even when the Host header carries one", () => {
    const origin = resolveCanonicalOrigin(requestWith({ host: "0.0.0.0:3000" }));

    expect(origin).not.toContain("0.0.0.0");
    expect(origin).toBe("http://localhost:3000");
  });

  it("prefers a non-loopback NEXT_PUBLIC_APP_URL over any request header", () => {
    process.env.NEXT_PUBLIC_APP_URL = "https://chat-hive.scubed.co";

    expect(
      resolveCanonicalOrigin(requestWith({ "x-forwarded-host": "evil.test" })),
    ).toBe("https://chat-hive.scubed.co");
  });

  it("does not let a stale loopback NEXT_PUBLIC_APP_URL poison a deployed origin", () => {
    process.env.NEXT_PUBLIC_APP_URL = "http://localhost:3000";

    expect(
      resolveCanonicalOrigin(
        requestWith({
          "x-forwarded-host": "chat-hive.scubed.co",
          "x-forwarded-proto": "https",
        }),
      ),
    ).toBe("https://chat-hive.scubed.co");
  });

  it("keeps a loopback NEXT_PUBLIC_APP_URL when no forwarded host is present", () => {
    process.env.NEXT_PUBLIC_APP_URL = "http://localhost:3000";

    expect(resolveCanonicalOrigin(requestWith({}))).toBe("http://localhost:3000");
  });

  it("ignores an unparseable forwarded host rather than splicing it into a Location", () => {
    expect(
      resolveCanonicalOrigin(requestWith({ "x-forwarded-host": "not a host/../evil" })),
    ).toBe("http://localhost:3000");
  });
});
