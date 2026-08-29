import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { resolveClientOrigin } from "./client-origin";

// Issue #487's guard.
//
// The failure it describes is silent by construction: NEXT_PUBLIC_APP_URL is
// inlined into the client bundle at build time, `.env.example` ships it as
// http://localhost:3000, and the only symptom is that a user who was mailed a
// link cannot use it. Nobody clicks their own password-reset email, so nothing
// internal notices. These assertions are the thing that notices.

const originalAppUrl = process.env.NEXT_PUBLIC_APP_URL;
const originalLocation = Object.getOwnPropertyDescriptor(window, "location");

function setPageOrigin(origin: string): void {
  Object.defineProperty(window, "location", {
    configurable: true,
    writable: true,
    value: { origin } as Location,
  });
}

beforeEach(() => {
  setPageOrigin("https://console-hive.example.co");
});

afterEach(() => {
  if (originalAppUrl === undefined) {
    delete process.env.NEXT_PUBLIC_APP_URL;
  } else {
    process.env.NEXT_PUBLIC_APP_URL = originalAppUrl;
  }
  if (originalLocation) {
    Object.defineProperty(window, "location", originalLocation);
  }
});

describe("resolveClientOrigin", () => {
  it("uses a real configured origin, which cannot be chosen by a request", () => {
    process.env.NEXT_PUBLIC_APP_URL = "https://console.example.test";
    expect(resolveClientOrigin()).toBe("https://console.example.test");
  });

  it("demotes a loopback NEXT_PUBLIC_APP_URL below the page's own origin", () => {
    // This is the exact shape of issue #487: the value `.env.example` ships,
    // carried forward into a deployed build.
    process.env.NEXT_PUBLIC_APP_URL = "http://localhost:3000";
    expect(resolveClientOrigin()).toBe("https://console-hive.example.co");
  });

  it("demotes every loopback spelling, not just localhost", () => {
    for (const loopback of [
      "http://127.0.0.1:3000",
      "http://localhost",
      "http://[::1]:3000",
    ]) {
      process.env.NEXT_PUBLIC_APP_URL = loopback;
      expect(resolveClientOrigin()).toBe("https://console-hive.example.co");
    }
  });

  it("rejects a wildcard bind address, which is never followable", () => {
    process.env.NEXT_PUBLIC_APP_URL = "http://0.0.0.0:3000";
    expect(resolveClientOrigin()).toBe("https://console-hive.example.co");
  });

  it("ignores an unparseable configured value rather than splicing it into a link", () => {
    process.env.NEXT_PUBLIC_APP_URL = "not a url";
    expect(resolveClientOrigin()).toBe("https://console-hive.example.co");
  });

  it("keeps the loopback origin for local development, where the page is also loopback", () => {
    process.env.NEXT_PUBLIC_APP_URL = "http://localhost:3000";
    setPageOrigin("http://localhost:3000");
    expect(resolveClientOrigin()).toBe("http://localhost:3000");
  });

  it("falls back to the loopback default when nothing else is available", () => {
    delete process.env.NEXT_PUBLIC_APP_URL;
    setPageOrigin("");
    expect(resolveClientOrigin()).toBe("http://localhost:3000");
  });

  // Every call site today is inside a browser event handler, so there is always
  // a window. Review asked what happens without one, which is a fair question
  // about a helper anybody can import: with no page origin to demote to, a
  // loopback configured value is all there is, and returning it is correct for
  // the one context that has no window and a loopback config, which is local
  // development. Pinned here so the answer is a decision rather than an
  // accident if somebody calls this from a server component.
  it("returns the configured loopback origin when there is no window to prefer", () => {
    process.env.NEXT_PUBLIC_APP_URL = "http://localhost:3000";
    vi.stubGlobal("window", undefined);
    try {
      expect(resolveClientOrigin()).toBe("http://localhost:3000");
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("falls back to the default with neither a window nor a configured origin", () => {
    delete process.env.NEXT_PUBLIC_APP_URL;
    vi.stubGlobal("window", undefined);
    try {
      expect(resolveClientOrigin()).toBe("http://localhost:3000");
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
