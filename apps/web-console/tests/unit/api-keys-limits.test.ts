import { describe, expect, it } from "vitest";
import {
  parseKeyLimits,
  parseKeyLimitsInput,
  RATE_LIMIT_RPM_MAX,
  RATE_LIMIT_TPM_MAX,
  TIER_NAMES,
  validateLimits,
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

// Transport now lives in lib/control-plane/client.ts and is covered by
// __tests__/api-key-limits-client.test.ts (issue #552).
describe("parseKeyLimitsInput", () => {
  it("rejects payloads that are not a limits input", () => {
    expect(parseKeyLimitsInput(null)).toBeNull();
    expect(parseKeyLimitsInput("60")).toBeNull();
    expect(parseKeyLimitsInput([60, 4000])).toBeNull();
    expect(parseKeyLimitsInput({ rpm: 60 })).toBeNull();
    expect(parseKeyLimitsInput({ rpm: "60", tpm: 4000 })).toBeNull();
  });

  it("accepts a valid input and drops unknown tiers", () => {
    const parsed = parseKeyLimitsInput({
      rpm: 60,
      tpm: 4000,
      tier_overrides: {
        verified: { rpm: 30, tpm: 2000 },
        platinum: { rpm: 1, tpm: 1 },
      },
    });
    expect(parsed).toEqual({
      rpm: 60,
      tpm: 4000,
      tier_overrides: { verified: { rpm: 30, tpm: 2000 } },
    });
  });

  it("treats a missing override map as no overrides", () => {
    expect(parseKeyLimitsInput({ rpm: 0, tpm: 0 })).toEqual({
      rpm: 0,
      tpm: 0,
      tier_overrides: {},
    });
  });
});

describe("TIER_NAMES exhaustiveness", () => {
  it("covers the four hot-path tiers", () => {
    expect(TIER_NAMES).toEqual(["guest", "unverified", "verified", "credited"]);
  });
});
