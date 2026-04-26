import { describe, expect, it, vi } from "vitest";
import {
  getKeyLimits,
  parseKeyLimits,
  RATE_LIMIT_RPM_MAX,
  RATE_LIMIT_TPM_MAX,
  TIER_NAMES,
  updateKeyLimits,
  validateLimits,
  type ApiKeysClient,
} from "@/lib/api-keys";

describe("parseKeyLimits", () => {
  it("returns null for invalid payloads", () => {
    expect(parseKeyLimits(null)).toBeNull();
    expect(parseKeyLimits("nope")).toBeNull();
    expect(parseKeyLimits({ rpm: 60 })).toBeNull();
    expect(parseKeyLimits({ api_key_id: "x", rpm: "not-a-number", tpm: 0 })).toBeNull();
  });

  it("filters unknown tier names from the override map", () => {
    const out = parseKeyLimits({
      api_key_id: "k1",
      rpm: 60,
      tpm: 4000,
      tier_overrides: {
        verified: { rpm: 1, tpm: 2 },
        platinum: { rpm: 5, tpm: 6 },
      },
    });
    expect(out).not.toBeNull();
    expect(out?.tier_overrides.verified).toEqual({ rpm: 1, tpm: 2 });
    expect(Object.keys(out?.tier_overrides ?? {})).not.toContain("platinum");
  });
});

describe("validateLimits", () => {
  it("rejects out-of-range RPM/TPM", () => {
    expect(validateLimits({ rpm: -1, tpm: 0, tier_overrides: {} })).toContain("RPM");
    expect(validateLimits({ rpm: RATE_LIMIT_RPM_MAX + 1, tpm: 0, tier_overrides: {} })).toContain("RPM");
    expect(validateLimits({ rpm: 0, tpm: -5, tier_overrides: {} })).toContain("TPM");
    expect(validateLimits({ rpm: 0, tpm: RATE_LIMIT_TPM_MAX + 1, tier_overrides: {} })).toContain("TPM");
  });

  it("accepts valid limits", () => {
    expect(validateLimits({ rpm: 60, tpm: 4000, tier_overrides: { verified: { rpm: 30, tpm: 2000 } } })).toBeNull();
  });

  it("validates tier ranges", () => {
    expect(
      validateLimits({
        rpm: 1,
        tpm: 1,
        tier_overrides: { verified: { rpm: -1, tpm: 0 } },
      }),
    ).toContain("verified RPM");
  });
});

describe("api-keys client", () => {
  const stubClient = (resp: Response): ApiKeysClient => ({
    fetch: vi.fn().mockResolvedValue(resp),
  });

  it("getKeyLimits parses the response", async () => {
    const body = {
      api_key_id: "k1",
      rpm: 100,
      tpm: 5000,
      tier_overrides: { verified: { rpm: 80, tpm: 4000 } },
    };
    const c = stubClient(new Response(JSON.stringify(body), { status: 200 }));
    const out = await getKeyLimits(c, "k1");
    expect(out.rpm).toBe(100);
    expect(out.tier_overrides.verified?.rpm).toBe(80);
  });

  it("updateKeyLimits validates first and short-circuits", async () => {
    const c = stubClient(new Response("ignored", { status: 200 }));
    await expect(
      updateKeyLimits(c, "k1", { rpm: -1, tpm: 0, tier_overrides: {} }),
    ).rejects.toThrow(/RPM/);
    expect(c.fetch).not.toHaveBeenCalled();
  });

  it("updateKeyLimits surfaces non-OK status", async () => {
    const c = stubClient(new Response("nope", { status: 422 }));
    await expect(
      updateKeyLimits(c, "k1", { rpm: 1, tpm: 1, tier_overrides: {} }),
    ).rejects.toThrow(/422/);
  });
});

describe("TIER_NAMES exhaustiveness", () => {
  it("covers the four hot-path tiers", () => {
    expect(TIER_NAMES).toEqual(["guest", "unverified", "verified", "credited"]);
  });
});
