/**
 * getRequestContext() (lib/control-plane/client.ts) retries supabase.auth.getUser()
 * once before giving up: a transient upstream hiccup (the failure class that
 * crashed the budget settings page in CI -- console-budgets.spec.ts failed
 * with the console's generic error boundary instead of the budget form)
 * usually clears on a second attempt a moment later.
 *
 * This guards the retry bound on both sides: a transient failure followed by
 * success must not surface as an error, and a call that keeps failing must
 * not retry a third time (that would be a silent behavior change, and an
 * unbounded retry on a genuinely revoked token is its own bug).
 *
 * Does not (and cannot, in this jsdom-based vitest environment) exercise
 * getRequestContext()'s React cache() memoization: that dedup only activates
 * under the "react-server" module condition Next.js's bundler selects for
 * real Server Components, which the client "react" build resolved here does
 * not apply outside of it. See the PR discussion this test's commit
 * accompanies for why a jsdom test asserting that specifically was written,
 * found to fail identically with and without the fix, and removed rather
 * than shipped.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";

const mockGetUser = vi.fn();
const mockGetSession = vi.fn();

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

vi.mock("../lib/supabase/server", () => ({
  createClient: vi.fn(() => ({
    auth: { getUser: mockGetUser, getSession: mockGetSession },
  })),
}));

const BASE_URL = "http://control-plane.internal:8081";

const VIEWER_PAYLOAD = {
  user: { id: "u1", email: "owner@example.com", email_verified: true },
  current_account: {
    id: "acc1",
    display_name: "Acme",
    account_type: "workspace",
    role: "owner",
  },
  memberships: [],
  permissions: [],
};

describe("getRequestContext retries getUser once on a transient failure", () => {
  beforeEach(() => {
    vi.resetModules();
    vi.clearAllMocks();
    process.env.CONTROL_PLANE_BASE_URL = BASE_URL;
    mockGetSession.mockResolvedValue({
      data: { session: { access_token: "<ACCESS_TOKEN>" } },
    });
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(new Response(JSON.stringify(VIEWER_PAYLOAD), { status: 200 }))
      )
    );
  });

  it("succeeds when the first getUser call fails and the second succeeds", async () => {
    mockGetUser
      .mockResolvedValueOnce({ data: { user: null }, error: new Error("upstream hiccup") })
      .mockResolvedValueOnce({ data: { user: { id: "u1" } }, error: null });

    const client = await import("../lib/control-plane/client");

    await expect(client.getViewer()).resolves.toMatchObject({
      user: { id: "u1" },
    });
    expect(mockGetUser).toHaveBeenCalledTimes(2);
  });

  it("throws after the second getUser call also fails, without a third attempt", async () => {
    mockGetUser.mockResolvedValue({
      data: { user: null },
      error: new Error("still failing"),
    });

    const client = await import("../lib/control-plane/client");

    await expect(client.getViewer()).rejects.toThrow();
    expect(mockGetUser).toHaveBeenCalledTimes(2);
  });

  it("makes exactly one getUser call when the first attempt already succeeds", async () => {
    mockGetUser.mockResolvedValue({ data: { user: { id: "u1" } }, error: null });

    const client = await import("../lib/control-plane/client");

    await client.getViewer();
    expect(mockGetUser).toHaveBeenCalledTimes(1);
  });
});
