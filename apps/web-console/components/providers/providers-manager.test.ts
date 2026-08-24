import { describe, expect, it } from "vitest";

import { providerPutBody } from "./providers-manager";
import type { CustomProvider } from "@/lib/control-plane/client";

// The upstream PUT /api/v1/admin/providers/{id} replaces the whole record
// (apps/control-plane/internal/providers/http.go): there is no partial
// update. A body missing a field silently wipes that column server-side, so
// this test pins the field set of the single builder both the edit form and
// the enabled toggle route through.
const row: CustomProvider = {
  id: "11111111-1111-4111-8111-111111111111",
  slug: "together-ai",
  display_name: "Together AI",
  base_url: "https://api.together.xyz/v1",
  api_key_env: "TOGETHER_API_KEY",
  litellm_prefix: "together_ai/",
  enabled: true,
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-01T00:00:00Z",
};

describe("providerPutBody (full-replace PUT semantics)", () => {
  it("echoes back every persisted field so the PUT cannot wipe one", () => {
    const body = providerPutBody(row, false);

    expect(body).toEqual({
      slug: "together-ai",
      display_name: "Together AI",
      base_url: "https://api.together.xyz/v1",
      api_key_env: "TOGETHER_API_KEY",
      litellm_prefix: "together_ai/",
      enabled: false,
    });
  });

  it("carries the requested enabled state through unchanged fields", () => {
    expect(providerPutBody(row, true).enabled).toBe(true);
  });

  it("trims whitespace on every string field", () => {
    const body = providerPutBody(
      { ...row, display_name: "  Together AI  ", slug: " together-ai " },
      true,
    );

    expect(body.slug).toBe("together-ai");
    expect(body.display_name).toBe("Together AI");
  });
});
