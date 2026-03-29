import { describe, expect, it } from "vitest";
import { accountProfileSchema } from "@/lib/profile-schemas";

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
