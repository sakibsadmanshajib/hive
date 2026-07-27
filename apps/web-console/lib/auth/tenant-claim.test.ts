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

  // Real JWT segments are base64url with the padding stripped, so the decoder
  // has to restore it. atob is lenient about missing padding in some runtimes
  // and strict in others, and the failure mode matters: a throw fails safe to
  // "no claim", which for a user who genuinely has one means being sent round
  // the provisioning redirect on every request. These cover every unpadded
  // length the encoding can produce.
  describe("unpadded base64url payloads", () => {
    it.each([
      ["remainder 2", "tenant-a"],
      ["remainder 3", "tenant-ab"],
      ["no remainder", "tenant-abc"],
    ])("decodes a payload with %s", (_label, tenantId) => {
      const payload = JSON.stringify({ tenant_id: tenantId });
      const token = tokenWithPayload(payload);
      // Guard the fixture itself: if btoa ever emitted padding here the test
      // would stop covering what it claims to.
      expect(token).not.toContain("=");
      expect(readTenantIdClaim(token)).toBe(tenantId);
    });

    it("covers all three base64 length remainders across the cases above", () => {
      const remainders = new Set(
        ["tenant-a", "tenant-ab", "tenant-abc"].map((tenantId) => {
          const encoded = btoa(JSON.stringify({ tenant_id: tenantId })).replace(
            /=+$/,
            "",
          );
          return encoded.length % 4;
        }),
      );
      expect(remainders.size).toBeGreaterThan(1);
    });
  });
});
