/**
 * The console proxy at app/api/v1/accounts/current/[...path]/route.ts maps an
 * upstream refusal onto its own status by branching on ControlPlaneError. That
 * branch is only reachable if these client functions actually raise one.
 *
 * They used to throw a plain Error built from readResponseError, so the branch
 * was dead and every upstream 4xx reached the browser as a 502: a member who is
 * not the workspace owner, an already revoked key and an out of range value all
 * read as an outage. Revert any of these to a plain Error and the matching case
 * here goes red.
 *
 * getBudgetThreshold is here for a second reason, and it is a render rather
 * than a proxy. billing.view is owner-only in authz.Policy, so a member's
 * answer on the billing overview is a 403, and app/console/billing/page.tsx
 * tells that refusal apart from an outage by asking `err instanceof
 * ControlPlaneError`. With a plain Error that test is false for every failure,
 * the refusal is classified as unreadable, and the page claims the budget
 * service is unreachable when it answered correctly (issue #494).
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

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

type Client = typeof import("../lib/control-plane/client");

const BASE_URL = "http://control-plane.internal:8081";
const KEY_ID = "11111111-1111-4111-8111-111111111111";

let nextResponse: Response = new Response("", { status: 200 });

const CALLS: { name: string; call: (client: Client) => Promise<unknown> }[] = [
  { name: "createApiKey", call: (client) => client.createApiKey("k") },
  { name: "revokeApiKey", call: (client) => client.revokeApiKey(KEY_ID) },
  { name: "getCheckoutRails", call: (client) => client.getCheckoutRails() },
  {
    name: "initiateCheckout",
    call: (client) => client.initiateCheckout("bkash", 1000, "idem-1"),
  },
  { name: "getBudgetThreshold", call: (client) => client.getBudgetThreshold() },
];

describe("control-plane client error shape for the browser proxy", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    process.env.CONTROL_PLANE_BASE_URL = BASE_URL;
    mockGetUser.mockResolvedValue({ data: { user: { id: "u1" } }, error: null });
    mockGetSession.mockResolvedValue({
      data: { session: { access_token: "<ACCESS_TOKEN>" } },
    });
    vi.stubGlobal("fetch", () => Promise.resolve(nextResponse));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  for (const { name, call } of CALLS) {
    it(name + " raises a ControlPlaneError carrying the upstream status", async () => {
      nextResponse = new Response(JSON.stringify({ error: "not the workspace owner" }), {
        status: 403,
      });
      const client = await import("../lib/control-plane/client");

      const failure = await call(client).catch((err: unknown): unknown => err);

      expect(failure).toBeInstanceOf(client.ControlPlaneError);
      if (failure instanceof client.ControlPlaneError) {
        expect(failure.status).toBe(403);
      }
    });
  }
});
