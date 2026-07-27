// @vitest-environment node
//
// Regression guard for the `0.0.0.0` redirect family.
//
// Next.js builds `request.url` from the server's OWN bind address, never from
// the Host header (next/dist/server/lib/router-utils/resolve-routes.js composes
// `initUrl` from `opts.hostname` + `opts.port`). Both console containers start
// with `--hostname 0.0.0.0`, so any route handler that derives an absolute
// redirect target from `request.url` emits `http(s)://0.0.0.0:3000/...` and
// strands the user.
//
// Every request below therefore carries the real shape of the bug: a request
// URL on the wildcard bind address plus the forwarded headers Caddy and
// Cloudflare Tunnel actually set. The assertions pin the emitted Location to
// the forwarded host and explicitly reject `0.0.0.0`.
//
// Node environment (not jsdom) because NextResponse rejects a request built
// with jsdom's Headers implementation.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { NextRequest } from "next/server";

const FORWARDED_HEADERS = {
  "x-forwarded-host": "console-hive.scubed.co",
  "x-forwarded-proto": "https",
} as const;

let exchangeResult: {
  error: { message: string } | null;
  data?: { session?: { access_token: string } };
} = { error: null };

vi.mock("@supabase/ssr", () => ({
  createServerClient: () => ({
    auth: {
      exchangeCodeForSession: () => Promise.resolve(exchangeResult),
    },
  }),
}));

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ getAll: () => [], set: () => {} }),
}));

let memberships: Array<{ account_id: string }> = [];
let viewerThrows = false;

vi.mock("@/lib/control-plane/client", () => ({
  getViewer: () => {
    if (viewerThrows) return Promise.reject(new Error("control-plane down"));
    return Promise.resolve({ memberships });
  },
}));

vi.mock("@/lib/supabase/server", () => ({
  createClient: () => ({ auth: { signOut: () => Promise.resolve() } }),
}));

import { GET as authCallback } from "../app/auth/callback/route";
import { POST as accountSwitch } from "../app/console/account-switch/route";

// Snapshot every variable these tests mutate, so the suite cannot leak fake or
// missing configuration into another suite sharing the same worker.
const MUTATED_ENV_KEYS = [
  "NEXT_PUBLIC_SUPABASE_URL",
  "NEXT_PUBLIC_SUPABASE_ANON_KEY",
  "NEXT_PUBLIC_APP_URL",
  "CONTROL_PLANE_BASE_URL",
];
const ORIGINAL_ENV = new Map<string, string | undefined>(
  MUTATED_ENV_KEYS.map((key) => [key, process.env[key]]),
);

function restoreEnv(): void {
  for (const [key, value] of ORIGINAL_ENV) {
    if (value === undefined) {
      delete process.env[key];
    } else {
      process.env[key] = value;
    }
  }
}

function wildcardRequest(path: string): NextRequest {
  return new NextRequest(`http://0.0.0.0:3000${path}`, {
    headers: { ...FORWARDED_HEADERS },
  });
}

function switchRequest(accountId: string | null): NextRequest {
  const body = new FormData();
  if (accountId !== null) body.set("account_id", accountId);
  return new NextRequest("http://0.0.0.0:3000/console/account-switch", {
    method: "POST",
    body,
    headers: { ...FORWARDED_HEADERS },
  });
}

beforeEach(() => {
  exchangeResult = { error: null };
  memberships = [];
  viewerThrows = false;
  process.env.NEXT_PUBLIC_SUPABASE_URL = "https://test.supabase.co";
  process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY = "anon-key-test";
  // Deliberately the stale value shipped in .env.example. A deployment that
  // carries it forward must still redirect to the real forwarded host.
  process.env.NEXT_PUBLIC_APP_URL = "http://localhost:3000";
  delete process.env.CONTROL_PLANE_BASE_URL;
});

afterEach(restoreEnv);

describe("app/auth/callback/route.ts redirect origin", () => {
  it("sends a successful exchange to the forwarded host, not the wildcard bind address", async () => {
    const response = await authCallback(wildcardRequest("/auth/callback?code=abc"));

    expect(response.headers.get("location")).toBe(
      "https://console-hive.scubed.co/console",
    );
  });

  it("sends a failed exchange to the forwarded host, not the wildcard bind address", async () => {
    exchangeResult = { error: { message: "invalid code" } };

    const response = await authCallback(wildcardRequest("/auth/callback?code=abc"));

    expect(response.headers.get("location")).toBe(
      "https://console-hive.scubed.co/console",
    );
  });

  it("sends a request with no code to the forwarded host", async () => {
    const response = await authCallback(wildcardRequest("/auth/callback"));

    expect(response.headers.get("location")).toBe(
      "https://console-hive.scubed.co/console",
    );
  });

  it("ignores a forwarded proto that is not http or https", async () => {
    // The resolved origin lands in a Location header, so a scheme taken from the
    // header as-is would let a caller that reaches the app without a proxy
    // overwriting X-Forwarded-Proto emit a non-http redirect. Anything outside
    // http/https must fall back to the host-derived scheme instead.
    const response = await authCallback(
      new NextRequest("http://0.0.0.0:3000/auth/callback?code=abc", {
        headers: {
          "x-forwarded-host": "console-hive.scubed.co",
          "x-forwarded-proto": "javascript",
        },
      }),
    );

    expect(response.headers.get("location")).toBe(
      "https://console-hive.scubed.co/console",
    );
  });

  it("keeps resolving the allow-listed next target against the forwarded host", async () => {
    const response = await authCallback(
      wildcardRequest("/auth/callback?code=abc&next=/auth/reset-password"),
    );

    expect(response.headers.get("location")).toBe(
      "https://console-hive.scubed.co/auth/reset-password",
    );
  });

  it("still reads query params from the request even though the origin is discarded", async () => {
    // The origin fix must not cost the handler its searchParams: a next target
    // that stops being honoured would break password reset silently.
    const response = await authCallback(
      wildcardRequest(
        `/auth/callback?code=abc&next=${encodeURIComponent(
          "/oauth/consent?authorization_id=auth-req-123",
        )}`,
      ),
    );

    expect(response.headers.get("location")).toBe(
      "https://console-hive.scubed.co/oauth/consent?authorization_id=auth-req-123",
    );
  });

  it("never emits the wildcard bind address on any branch", async () => {
    exchangeResult = { error: { message: "nope" } };

    const response = await authCallback(wildcardRequest("/auth/callback?code=abc"));

    expect(response.headers.get("location")).not.toContain("0.0.0.0");
  });
});

describe("app/console/account-switch/route.ts redirect origin", () => {
  it("sends a missing account_id to the forwarded host", async () => {
    const response = await accountSwitch(switchRequest(null));

    expect(response.headers.get("location")).toBe(
      "https://console-hive.scubed.co/console",
    );
  });

  it("sends an unreadable viewer to the forwarded host", async () => {
    viewerThrows = true;

    const response = await accountSwitch(switchRequest("acct-1"));

    expect(response.headers.get("location")).toBe(
      "https://console-hive.scubed.co/console",
    );
  });

  it("sends a non-member account to the forwarded host", async () => {
    memberships = [{ account_id: "acct-other" }];

    const response = await accountSwitch(switchRequest("acct-1"));

    expect(response.headers.get("location")).toBe(
      "https://console-hive.scubed.co/console",
    );
  });

  it("sends a valid switch to the forwarded host and still sets the account cookie", async () => {
    memberships = [{ account_id: "acct-1" }];

    const response = await accountSwitch(switchRequest("acct-1"));

    expect(response.headers.get("location")).toBe(
      "https://console-hive.scubed.co/console",
    );
    // Non-vacuous: the fix must not cost the route its actual side effect.
    expect(response.cookies.get("hive_account_id")?.value).toBe("acct-1");
  });

  it("never emits the wildcard bind address on any branch", async () => {
    memberships = [{ account_id: "acct-1" }];

    const response = await accountSwitch(switchRequest("acct-1"));

    expect(response.headers.get("location")).not.toContain("0.0.0.0");
  });
});
