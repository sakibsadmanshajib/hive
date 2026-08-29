/**
 * Guard for the shape that produced the buy-credits defect on the deployed box.
 *
 * `GET /checkout/rails` carries a top level `max_credits` that the control
 * plane computes as the most restrictive ceiling among the rails the payer can
 * actually select. When nothing is selectable that value is 0, which is the
 * honest answer for a deployment with no rail credentials, and the console must
 * treat it as "no purchase is possible" rather than as a ceiling. When a rail
 * IS selectable, a maximum below the minimum is not a sentinel, it is a broken
 * response, and rendering it produced a live `min="10000000" max="0"` on the
 * amount input.
 *
 * Remove either branch of the guard in getCheckoutRails and one of these goes
 * red.
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

const BASE_URL = "http://control-plane.internal:8081";

function railsPayload(overrides: Record<string, unknown> = {}) {
  return {
    rails: [
      {
        rail: "stripe",
        label: "Card",
        currency: "USD",
        enabled: true,
        min_credits: 10_000_000,
        max_credits: 100_000_000_000,
      },
    ],
    predefined_tiers: [],
    price_per_block_minor: 100,
    credit_block_size: 1_000_000_000,
    currency: "USD",
    credit_increment: 10_000_000,
    min_credits: 10_000_000,
    max_credits: 100_000_000_000,
    ...overrides,
  };
}

function respondWith(payload: unknown) {
  vi.stubGlobal("fetch", () =>
    Promise.resolve(new Response(JSON.stringify(payload), { status: 200 })),
  );
}

describe("getCheckoutRails purchase range guard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    process.env.CONTROL_PLANE_BASE_URL = BASE_URL;
    mockGetUser.mockResolvedValue({ data: { user: { id: "u1" } }, error: null });
    mockGetSession.mockResolvedValue({
      data: { session: { access_token: "<ACCESS_TOKEN>" } },
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("passes a coherent range through untouched", async () => {
    respondWith(railsPayload());
    const client = await import("../lib/control-plane/client");

    const options = await client.getCheckoutRails();

    expect(options.min_credits).toBe(10_000_000);
    expect(options.max_credits).toBe(100_000_000_000);
    expect(options.rails).toHaveLength(1);
  });

  it("rejects a maximum below the minimum while a rail is selectable", async () => {
    respondWith(railsPayload({ max_credits: 0 }));
    const client = await import("../lib/control-plane/client");

    await expect(client.getCheckoutRails()).rejects.toThrow(/checkout rails/i);
  });

  it("rejects a non-positive minimum while a rail is selectable", async () => {
    respondWith(railsPayload({ min_credits: 0, max_credits: 0 }));
    const client = await import("../lib/control-plane/client");

    await expect(client.getCheckoutRails()).rejects.toThrow(/checkout rails/i);
  });

  it("keeps the zero ceiling when no rail is selectable, because that is the honest answer", async () => {
    respondWith(
      railsPayload({
        rails: [
          {
            rail: "stripe",
            label: "Card",
            currency: "USD",
            enabled: false,
            min_credits: 10_000_000,
            max_credits: 100_000_000_000,
          },
        ],
        max_credits: 0,
      }),
    );
    const client = await import("../lib/control-plane/client");

    const options = await client.getCheckoutRails();

    expect(options.rails.every((rail) => !rail.enabled)).toBe(true);
    expect(options.max_credits).toBe(0);
  });
});
