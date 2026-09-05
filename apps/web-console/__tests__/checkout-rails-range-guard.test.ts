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
        price_minor_numerator: 53,
        price_credits_denominator: 500_000_000,
      },
    ],
    predefined_tiers: [],
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


  // A rail carrying no price is dropped, exactly as one carrying no currency
  // is: the modal cannot render an amount it was not sent, and a purchase with
  // no visible price is the defect issue #1737 was filed about. Dropping the
  // only rail leaves nothing selectable, which the modal explains.
  for (const field of [
    "price_minor_numerator",
    "price_credits_denominator",
  ]) {
    it(`drops a rail with no ${field}`, async () => {
      const payload = railsPayload();
      const rails = payload.rails as Array<Record<string, unknown>>;
      delete rails[0][field];
      respondWith(payload);
      const client = await import("../lib/control-plane/client");

      const options = await client.getCheckoutRails();
      expect(options.rails).toHaveLength(0);
    });
  }

  it("drops a rail whose price denominator is zero", async () => {
    const payload = railsPayload();
    const rails = payload.rails as Array<Record<string, unknown>>;
    rails[0].price_credits_denominator = 0;
    respondWith(payload);
    const client = await import("../lib/control-plane/client");

    const options = await client.getCheckoutRails();
    expect(options.rails).toHaveLength(0);
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

  // An omitted bound is the quiet version of the same fault. Defaulting before
  // the coherence check would fabricate a valid range out of the absence and
  // wave it through, so the guard would catch the loud failure and miss the
  // silent one. That is how issue #1386 shipped a 1.00 USD ceiling against a
  // real 100.00 USD one with nothing complaining.
  for (const field of ["min_credits", "max_credits", "credit_increment"]) {
    it(`rejects an omitted ${field} while a rail is selectable`, async () => {
      const payload: Record<string, unknown> = railsPayload();
      delete payload[field];
      respondWith(payload);
      const client = await import("../lib/control-plane/client");

      await expect(client.getCheckoutRails()).rejects.toThrow(/checkout rails/i);
    });
  }

  it("rejects a non-finite bound while a rail is selectable", async () => {
    // JSON has no NaN literal, so a string reaches readNumberField as a
    // non-number and comes back null, which is the same absence case.
    respondWith(railsPayload({ max_credits: "lots" }));
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

  it("tolerates absent bounds only where nothing is purchasable", async () => {
    const payload: Record<string, unknown> = railsPayload({
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
    });
    delete payload.min_credits;
    delete payload.max_credits;
    delete payload.credit_increment;
    respondWith(payload);
    const client = await import("../lib/control-plane/client");

    // Resolves rather than throwing: no rail can complete a purchase, so the
    // bounds are never rendered and the modal explains the state instead.
    const options = await client.getCheckoutRails();

    expect(options.rails.every((rail) => !rail.enabled)).toBe(true);
  });
});
