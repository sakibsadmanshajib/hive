import { describe, expect, it } from "vitest";
import {
  accountProfileSchema,
  billingProfileSchema,
} from "@/lib/profile-schemas";

describe("accountProfileSchema", () => {
  it("accepts the required core profile fields", () => {
    const result = accountProfileSchema.safeParse({
      ownerName: "Alice Smith",
      loginEmail: "alice@example.com",
      accountName: "Acme Labs",
      accountType: "business",
      countryCode: "US",
      stateRegion: "CA",
    });

    expect(result.success).toBe(true);
  });

  it("rejects missing required core profile fields", () => {
    const result = accountProfileSchema.safeParse({
      ownerName: "",
      loginEmail: "alice@example.com",
      accountName: "",
      accountType: "personal",
      countryCode: "",
      stateRegion: "",
    });

    expect(result.success).toBe(false);
    if (result.success) {
      throw new Error("expected validation to fail");
    }

    expect(result.errors.ownerName).toBeDefined();
    expect(result.errors.accountName).toBeDefined();
    expect(result.errors.countryCode).toBeDefined();
    expect(result.errors.stateRegion).toBeDefined();
  });
});

describe("billingProfileSchema", () => {
  it("accepts partial business billing fields", () => {
    const result = billingProfileSchema.safeParse({
      accountType: "business",
      legalEntityName: "Acme Labs LLC",
      legalEntityType: "private_company",
    });

    expect(result.success).toBe(true);
    if (!result.success) {
      throw new Error("expected validation to succeed");
    }

    expect(result.data.legalEntityName).toBe("Acme Labs LLC");
    expect(result.data.vatNumber).toBe("");
  });

  it("defaults personal legal entity type to individual", () => {
    const result = billingProfileSchema.safeParse({
      accountType: "personal",
      billingContactEmail: "alice@example.com",
    });

    expect(result.success).toBe(true);
    if (!result.success) {
      throw new Error("expected validation to succeed");
    }

    expect(result.data.legalEntityType).toBe("individual");
  });

  it("rejects invalid optional email and tax identifiers", () => {
    const result = billingProfileSchema.safeParse({
      accountType: "business",
      billingContactEmail: "not-an-email",
      vatNumber: "***",
      taxIdValue: "***",
    });

    expect(result.success).toBe(false);
    if (result.success) {
      throw new Error("expected validation to fail");
    }

    expect(result.errors.billingContactEmail).toBeDefined();
    expect(result.errors.vatNumber).toBeDefined();
    expect(result.errors.taxIdValue).toBeDefined();
  });
});
