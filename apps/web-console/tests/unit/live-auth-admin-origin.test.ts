import { afterEach, describe, expect, it, vi } from "vitest";
import { mintSession } from "../e2e/support/live-auth.mjs";

// The admin API and the browser-facing origin are not the same host on the
// self-hosted deployment, and conflating them fails in two directions.
//
// deploy/docker/Caddyfile.supabase answers 404 to /auth/v1/admin/* on the
// PUBLIC listener by design, so a mint aimed at the console origin cannot
// work from outside the box: that is the wall issue #1531 hit, where every
// authenticated control failed with "generate_link ... (HTTP 404)". Aiming
// SUPABASE_URL itself at the internal listener would clear that wall and
// break the other half instead, because sessionCookies derives the cookie
// NAME from that value (`sb-<first-hostname-label>-auth-token`), so the app
// would read a cookie it never looks for and report a signed-out browser
// with no error anywhere.
//
// Hence SUPABASE_ADMIN_URL: the mint alone moves. These tests pin which of
// the two values the mint uses, since nothing else can catch the swap before
// a live run does.
const PUBLIC_ORIGIN = "https://console.invalid";
const ADMIN_ORIGIN = "http://caddy-supabase";
const GENERATE_LINK = "/auth/v1/admin/generate_link";
// Not one of PROTECTED_ACCOUNT_BASES, so the shared-account guard is not what
// is being measured here.
const EMAIL = "agent-workspace-coverage+unit-check@hive-e2e.invalid";

/**
 * Records the URLs a mint would open and refuses to open them. The throw is
 * what makes the first call observable: mintSession retries only on an expired
 * one-time token, so an unrelated error stops it at exactly one request.
 */
function armFetchTrap(): string[] {
  const seen: string[] = [];
  vi.stubEnv("SUPABASE_SERVICE_ROLE_KEY", "service-role-not-a-real-key");
  vi.stubEnv("SUPABASE_ANON_KEY", "anon-not-a-real-key");
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) => {
      seen.push(String(url));
      throw new Error("live-auth test: fetch trap");
    }),
  );
  return seen;
}

describe("mintSession chooses the origin that serves the admin API", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.unstubAllGlobals();
  });

  it("uses SUPABASE_ADMIN_URL, not SUPABASE_URL, when both are set", async () => {
    const seen = armFetchTrap();
    vi.stubEnv("SUPABASE_URL", PUBLIC_ORIGIN);
    vi.stubEnv("SUPABASE_ADMIN_URL", ADMIN_ORIGIN);

    await expect(mintSession({ email: EMAIL })).rejects.toThrow(/fetch trap/);

    expect(seen).toEqual([`${ADMIN_ORIGIN}${GENERATE_LINK}`]);
  });

  it("falls back to SUPABASE_URL when no admin origin is declared", async () => {
    const seen = armFetchTrap();
    vi.stubEnv("SUPABASE_URL", PUBLIC_ORIGIN);
    vi.stubEnv("SUPABASE_ADMIN_URL", "");

    await expect(mintSession({ email: EMAIL })).rejects.toThrow(/fetch trap/);

    expect(seen).toEqual([`${PUBLIC_ORIGIN}${GENERATE_LINK}`]);
  });

  it("tolerates a trailing slash on either value rather than doubling it", async () => {
    const seen = armFetchTrap();
    vi.stubEnv("SUPABASE_URL", `${PUBLIC_ORIGIN}/`);
    vi.stubEnv("SUPABASE_ADMIN_URL", `${ADMIN_ORIGIN}/`);

    await expect(mintSession({ email: EMAIL })).rejects.toThrow(/fetch trap/);

    expect(seen).toEqual([`${ADMIN_ORIGIN}${GENERATE_LINK}`]);
  });

  it("still requires SUPABASE_URL when the admin origin is only whitespace", async () => {
    armFetchTrap();
    vi.stubEnv("SUPABASE_URL", "");
    vi.stubEnv("SUPABASE_ADMIN_URL", "   ");

    await expect(mintSession({ email: EMAIL })).rejects.toThrow(
      /SUPABASE_URL is not set/,
    );
  });
});
