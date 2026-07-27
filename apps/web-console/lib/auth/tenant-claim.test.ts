import { describe, expect, it } from "vitest";
import { readTenantIdClaim } from "./tenant-claim";

// Builds an unsigned token shaped like a JWT. The claim reader never verifies
// the signature (see tenant-claim.ts), so no signing is needed here.
function tokenWithPayload(payload: string): string {
  const encoded = btoa(payload)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
  return `header.${encoded}.signature`;
}

describe("readTenantIdClaim", () => {
  it("returns the tenant_id claim when present", () => {
    const token = tokenWithPayload(
      JSON.stringify({
        sub: "user-1",
        tenant_id: "11111111-2222-3333-4444-555555555555",
        role: "member",
      }),
    );
    expect(readTenantIdClaim(token)).toBe(
      "11111111-2222-3333-4444-555555555555",
    );
  });

  it("returns null when the token carries no tenant_id claim", () => {
    const token = tokenWithPayload(JSON.stringify({ sub: "user-1" }));
    expect(readTenantIdClaim(token)).toBeNull();
  });

  it("returns null when tenant_id is an empty string", () => {
    const token = tokenWithPayload(JSON.stringify({ tenant_id: "" }));
    expect(readTenantIdClaim(token)).toBeNull();
  });

  it("returns null when tenant_id is not a string", () => {
    const token = tokenWithPayload(JSON.stringify({ tenant_id: 42 }));
    expect(readTenantIdClaim(token)).toBeNull();
  });

  it("decodes a payload containing non-ASCII claims without throwing", () => {
    const token = tokenWithPayload(
      // btoa rejects code points above 255, so pre-encode as UTF-8 bytes the
      // same way a real GoTrue token would carry them.
      String.fromCharCode(
        ...new TextEncoder().encode(
          JSON.stringify({ name: "মোহাম্মদ", tenant_id: "tenant-9" }),
        ),
      ),
    );
    expect(readTenantIdClaim(token)).toBe("tenant-9");
  });

  it("returns null for garbage input", () => {
    expect(readTenantIdClaim("not-a-token")).toBeNull();
  });

  it("returns null for an empty string", () => {
    expect(readTenantIdClaim("")).toBeNull();
  });

  it("returns null for null", () => {
    expect(readTenantIdClaim(null)).toBeNull();
  });

  it("returns null for undefined", () => {
    expect(readTenantIdClaim(undefined)).toBeNull();
  });

  it("returns null when the payload segment is not JSON", () => {
    expect(readTenantIdClaim(tokenWithPayload("plain text, not json"))).toBeNull();
  });

  it("returns null when the payload is valid JSON but not an object", () => {
    expect(readTenantIdClaim(tokenWithPayload("\"just-a-string\""))).toBeNull();
  });
});
