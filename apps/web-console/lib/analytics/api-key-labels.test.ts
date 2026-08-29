import { describe, expect, it } from "vitest";

import type { ApiKey } from "@/lib/control-plane/client";
import { UNATTRIBUTED_GROUP_KEY } from "@/lib/control-plane/contract";
import {
  apiKeysById,
  formatApiKeyGroup,
  resolveApiKeyGroup,
} from "./api-key-labels";

function key(overrides: Partial<ApiKey> = {}): ApiKey {
  return {
    id: "890883f4-8da5-474f-8f33-e803f2153c8a",
    nickname: "orchestrator-livecheck",
    status: "active",
    redacted_suffix: "0fae3a",
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
    expires_at: null,
    last_used_at: null,
    expiration_summary: { kind: "never", label: "Never expires" },
    budget_summary: { kind: "none", label: "No budget cap" },
    allowlist_summary: { mode: "all", group_names: [], label: "All models" },
    spend_credits: 0,
    budget_limit_credits: null,
    ...overrides,
  };
}

describe("resolveApiKeyGroup", () => {
  it("resolves a known key id to its nickname and masked tail", () => {
    const map = apiKeysById([key()]);
    expect(resolveApiKeyGroup(key().id, map)).toEqual({
      label: "orchestrator-livecheck",
      suffix: "0fae3a",
    });
  });

  it("labels the unattributed bucket without calling it a deleted key", () => {
    expect(resolveApiKeyGroup(UNATTRIBUTED_GROUP_KEY, apiKeysById([]))).toEqual({
      label: "Unattributed",
      suffix: "no key on record",
    });
  });

  it("labels an id with no matching key as deleted, keeping a readable stub", () => {
    const resolved = resolveApiKeyGroup(key().id, apiKeysById([]));
    expect(resolved.label).toBe("Deleted key");
    expect(resolved.suffix).toBe("890883f4");
  });
});

describe("formatApiKeyGroup", () => {
  it("renders the nickname with the masked tail beside it", () => {
    expect(formatApiKeyGroup(key().id, apiKeysById([key()]))).toBe(
      "orchestrator-livecheck (0fae3a)"
    );
  });

  it("renders the unattributed bucket as a bare label with no tail", () => {
    expect(formatApiKeyGroup(UNATTRIBUTED_GROUP_KEY, apiKeysById([]))).toBe(
      "Unattributed"
    );
  });

  it("bounds a nickname minted before the length cap existed", () => {
    // The analytics table cell and the chart axis tick both take this string
    // raw, and a key stored before issue #1400's cap landed can still carry
    // 5000 characters. Naming keys on this surface must not hand that defect
    // to a second table.
    const long = key({ nickname: "A".repeat(5000) });
    const rendered = formatApiKeyGroup(long.id, apiKeysById([long]));

    expect(rendered.length).toBeLessThan(80);
    expect(rendered).toContain("…");
    expect(rendered).toContain("0fae3a");
  });

  it("leaves a nickname within the cap untouched", () => {
    const ordinary = key({ nickname: "billing-prod-2026" });
    expect(formatApiKeyGroup(ordinary.id, apiKeysById([ordinary]))).toBe(
      "billing-prod-2026 (0fae3a)"
    );
  });

  it("keeps two keys that share a nickname distinguishable", () => {
    const a = key({ id: "aaaa1111-0000-0000-0000-000000000000", redacted_suffix: "aaaa" });
    const b = key({ id: "bbbb2222-0000-0000-0000-000000000000", redacted_suffix: "bbbb" });
    const map = apiKeysById([a, b]);
    expect(formatApiKeyGroup(a.id, map)).not.toBe(formatApiKeyGroup(b.id, map));
  });
});
