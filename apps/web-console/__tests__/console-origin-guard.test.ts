import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Issue #1457. Every mutating route under app/api/console accepts a plain
// <form method="POST"> and carries no CSRF token; nothing in this repository
// asserted that a forged cross-site post is refused. These tests are that
// assertion. If lib/http/same-origin.ts stops refusing, every case in the
// "refuses a cross-origin request" table goes red.

const CANONICAL = "https://console.example.test";

const mockGetUser = vi.fn();
const mockCreateClient = vi.fn(() => ({ auth: { getUser: mockGetUser } }));

const upstream = {
  createInvitation: vi.fn(),
  updateMemberRole: vi.fn(),
  setFeatureGate: vi.fn(),
  createMarketplaceEntry: vi.fn(),
  updateMarketplaceEntry: vi.fn(),
  deleteMarketplaceEntry: vi.fn(),
  setMarketplaceEntryEnabled: vi.fn(),
  createProvider: vi.fn(),
  updateProvider: vi.fn(),
};

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({ get: vi.fn(() => undefined), getAll: vi.fn(() => []) })),
}));

vi.mock("../lib/supabase/server", () => ({
  createClient: mockCreateClient,
}));

vi.mock("../lib/control-plane/client", () => ({
  ...upstream,
  ControlPlaneError: class ControlPlaneError extends Error {
    status: number;
    code: string;
    constructor(status: number, message: string) {
      super(message);
      this.name = "ControlPlaneError";
      this.status = status;
      this.code = "";
    }
  },
}));

function jsonRequest(body: Record<string, unknown>, origin: string | null): Request {
  const headers = new Headers({ "Content-Type": "application/json" });
  if (origin !== null) headers.set("Origin", origin);
  return new Request("http://console.invalid/api/console", {
    method: "POST",
    headers,
    body: JSON.stringify(body),
  });
}

function formRequest(fields: Record<string, string>, origin: string | null): Request {
  const headers = new Headers({ "Content-Type": "application/x-www-form-urlencoded" });
  if (origin !== null) headers.set("Origin", origin);
  return new Request("http://console.invalid/api/console", {
    method: "POST",
    headers,
    body: new URLSearchParams(fields).toString(),
  });
}

function bareRequest(origin: string | null): Request {
  const headers = new Headers();
  if (origin !== null) headers.set("Origin", origin);
  return new Request("http://console.invalid/api/console", { method: "POST", headers });
}

function params(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

interface GuardedHandler {
  // The label a failure reports, so a red run names the route immediately.
  readonly name: string;
  // Invokes the real handler with an otherwise-valid request carrying `origin`.
  // Nothing but the Origin header differs between the allowed and refused
  // cases, so a 403 can only have come from the guard.
  readonly invoke: (origin: string | null) => Promise<Response>;
  // The control-plane call the handler would make if the guard let it through.
  readonly upstreamCall: ReturnType<typeof vi.fn>;
}

const handlers: readonly GuardedHandler[] = [
  {
    name: "app/api/console/members/route.ts POST",
    invoke: async (origin) => {
      const { POST } = await import("../app/api/console/members/route");
      return POST(formRequest({ email: "teammate@example.com" }, origin));
    },
    upstreamCall: upstream.createInvitation,
  },
  {
    name: "app/api/console/members/role/route.ts POST",
    invoke: async (origin) => {
      const { POST } = await import("../app/api/console/members/role/route");
      return POST(formRequest({ user_id: "u2", role: "owner" }, origin));
    },
    upstreamCall: upstream.updateMemberRole,
  },
  {
    name: "app/api/console/feature-gates/route.ts PUT",
    invoke: async (origin) => {
      const { PUT } = await import("../app/api/console/feature-gates/route");
      return PUT(jsonRequest({ key: "chat", enabled: true }, origin));
    },
    upstreamCall: upstream.setFeatureGate,
  },
  {
    name: "app/api/console/marketplace/route.ts POST",
    invoke: async (origin) => {
      const { POST } = await import("../app/api/console/marketplace/route");
      return POST(jsonRequest({ kind: "tool", name: "Scraper" }, origin));
    },
    upstreamCall: upstream.createMarketplaceEntry,
  },
  {
    name: "app/api/console/marketplace/[id]/route.ts PUT",
    invoke: async (origin) => {
      const { PUT } = await import("../app/api/console/marketplace/[id]/route");
      return PUT(jsonRequest({ name: "Scraper" }, origin), params("m1"));
    },
    upstreamCall: upstream.updateMarketplaceEntry,
  },
  {
    name: "app/api/console/marketplace/[id]/route.ts DELETE",
    invoke: async (origin) => {
      const { DELETE } = await import("../app/api/console/marketplace/[id]/route");
      return DELETE(bareRequest(origin), params("m1"));
    },
    upstreamCall: upstream.deleteMarketplaceEntry,
  },
  {
    name: "app/api/console/marketplace/[id]/enable/route.ts PUT",
    invoke: async (origin) => {
      const { PUT } = await import("../app/api/console/marketplace/[id]/enable/route");
      return PUT(jsonRequest({ enabled: true }, origin), params("m1"));
    },
    upstreamCall: upstream.setMarketplaceEntryEnabled,
  },
  {
    name: "app/api/console/providers/route.ts POST",
    invoke: async (origin) => {
      const { POST } = await import("../app/api/console/providers/route");
      return POST(jsonRequest({ slug: "acme", api_key_env: "ACME_API_KEY" }, origin));
    },
    upstreamCall: upstream.createProvider,
  },
  {
    name: "app/api/console/providers/[id]/route.ts PUT",
    invoke: async (origin) => {
      const { PUT } = await import("../app/api/console/providers/[id]/route");
      return PUT(
        jsonRequest({ slug: "acme", api_key_env: "ACME_API_KEY" }, origin),
        params("p1"),
      );
    },
    upstreamCall: upstream.updateProvider,
  },
];

describe("console mutating routes refuse a cross-origin request (issue #1457)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.stubEnv("NEXT_PUBLIC_APP_URL", CANONICAL);
    mockGetUser.mockResolvedValue({
      data: { user: { id: "u1", email: "owner@hive.com" } },
      error: null,
    });
    upstream.createInvitation.mockResolvedValue(undefined);
    upstream.updateMemberRole.mockResolvedValue(undefined);
    upstream.setFeatureGate.mockResolvedValue({ key: "chat", enabled: true });
    upstream.createMarketplaceEntry.mockResolvedValue({ id: "m1" });
    upstream.updateMarketplaceEntry.mockResolvedValue({ id: "m1" });
    upstream.deleteMarketplaceEntry.mockResolvedValue(undefined);
    upstream.setMarketplaceEntryEnabled.mockResolvedValue({ id: "m1", enabled: true });
    upstream.createProvider.mockResolvedValue({ id: "p1" });
    upstream.updateProvider.mockResolvedValue({ id: "p1" });
  });

  afterEach(() => {
    vi.unstubAllEnvs();
  });

  for (const handler of handlers) {
    it(`${handler.name} refuses a cross-origin request with 403`, async () => {
      const res = await handler.invoke("https://evil.example");

      expect(res.status).toBe(403);
      const body: unknown = await res.json();
      expect(body).toEqual({ error: "Forbidden" });
      // The refusal has to land before anything reaches the control-plane.
      expect(handler.upstreamCall).not.toHaveBeenCalled();
    });

    it(`${handler.name} refuses a present-but-empty Origin`, async () => {
      const res = await handler.invoke("");

      expect(res.status).toBe(403);
      expect(handler.upstreamCall).not.toHaveBeenCalled();
    });

    it(`${handler.name} accepts a same-origin request`, async () => {
      const res = await handler.invoke(CANONICAL);

      expect(res.status).not.toBe(403);
      expect(handler.upstreamCall).toHaveBeenCalled();
    });

    // The pinned decision: an absent Origin is treated as same-origin, because
    // older browsers omit it on same-origin form posts and these routes exist
    // to serve a no-JavaScript <form method="POST">.
    it(`${handler.name} accepts a request with no Origin header`, async () => {
      const res = await handler.invoke(null);

      expect(res.status).not.toBe(403);
      expect(handler.upstreamCall).toHaveBeenCalled();
    });
  }

  it("refuses a cross-origin request before it costs a session lookup", async () => {
    await handlers[0].invoke("https://evil.example");

    expect(mockGetUser).not.toHaveBeenCalled();
  });
});

// Applying the guard to some routes and not others is worse than not applying
// it, because a partial posture reads as protection. This walks the route tree
// on disk rather than trusting a hand-maintained list, so a mutating route
// added later fails here instead of shipping unguarded.
describe("every mutating console route is guarded", () => {
  // Resolved from the working directory rather than import.meta.url, which the
  // test transform does not give as a file URL. Both candidates are covered so
  // the walk works whether vitest was started in the package or at the repo
  // root; an empty result is caught by the first assertion below.
  const consoleApiDir =
    [
      join(process.cwd(), "app/api/console"),
      join(process.cwd(), "apps/web-console/app/api/console"),
    ].find((candidate) => existsSync(candidate)) ?? "";

  function routeFiles(dir: string): string[] {
    const found: string[] = [];
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) found.push(...routeFiles(path));
      else if (entry.name === "route.ts") found.push(path);
    }
    return found;
  }

  const files = consoleApiDir === "" ? [] : routeFiles(consoleApiDir);

  it("finds the route tree", () => {
    // A walk that silently found nothing would make every assertion below pass
    // vacuously. Eight files carry the nine handlers enumerated above.
    expect(consoleApiDir).not.toBe("");
    expect(files.length).toBeGreaterThanOrEqual(8);
  });

  it("finds every mutating handler and sees the guard in each file", () => {
    const mutating = /export\s+(?:async\s+)?function\s+(POST|PUT|PATCH|DELETE)\b/g;
    let handlerCount = 0;
    const unguarded: string[] = [];

    for (const file of files) {
      const source = readFileSync(file, "utf8");
      const matches = source.match(mutating);
      if (!matches) continue;
      handlerCount += matches.length;
      if (
        !source.includes("refuseCrossOrigin(request)") ||
        !source.includes("@/lib/http/same-origin")
      ) {
        unguarded.push(file);
      }
    }

    expect(unguarded).toEqual([]);
    expect(handlerCount).toBeGreaterThanOrEqual(handlers.length);
  });
});
