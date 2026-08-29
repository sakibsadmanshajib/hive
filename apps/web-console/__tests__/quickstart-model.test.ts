import { describe, expect, it } from "vitest";

import type { CatalogModel } from "@/lib/control-plane/client";
import { pickQuickstartAlias } from "@/lib/quickstart-model";

function model(
  id: string,
  pricingMode: string,
  badges: string[],
): CatalogModel {
  return {
    id,
    display_name: id,
    summary: "",
    capability_badges: badges,
    pricing: {
      input_price_credits: pricingMode === "fixed" ? 1 : null,
      output_price_credits: pricingMode === "fixed" ? 1 : null,
      cache_read_price_credits: null,
      cache_write_price_credits: null,
      pricing_mode: pricingMode,
    },
    lifecycle: "stable",
  };
}

// The catalog as the live deployment serves it, variable-price router first.
// That ordering is what put a router the quickstart could not run into every
// snippet on /console/docs (issue #1372).
const liveOrder: CatalogModel[] = [
  model("hive-auto", "upstream_actual", ["stable", "chat", "responses"]),
  model("hive-default", "fixed", ["stable", "chat", "responses"]),
  model("hive-embedding-default", "fixed", ["stable", "embeddings"]),
];

describe("pickQuickstartAlias", () => {
  it("does not name a variable-price alias even when it sorts first", () => {
    expect(pickQuickstartAlias(liveOrder)).toBe("hive-default");
  });

  it("does not name an embeddings alias in a chat snippet", () => {
    const embeddingsFirst: CatalogModel[] = [
      model("hive-embedding-default", "fixed", ["stable", "embeddings"]),
      model("hive-small", "fixed", ["stable", "chat"]),
    ];
    expect(pickQuickstartAlias(embeddingsFirst)).toBe("hive-small");
  });

  it("falls back to a Hive alias when no fixed-price chat alias exists", () => {
    expect(pickQuickstartAlias([liveOrder[0]])).toBe("hive-auto");
  });

  it("keeps that fallback chat-capable when an embeddings alias sorts first", () => {
    const embeddingsBeforeVariable: CatalogModel[] = [
      model("hive-embedding-default", "fixed", ["stable", "embeddings"]),
      model("hive-auto", "upstream_actual", ["stable", "chat", "responses"]),
    ];
    expect(pickQuickstartAlias(embeddingsBeforeVariable)).toBe("hive-auto");
  });

  it("falls back to the seeded alias when the catalog could not be read", () => {
    expect(pickQuickstartAlias([])).toBe("hive-default");
  });

  // The returned id is printed in a command on the API keys page and on
  // /console/docs. Upstream ids name the provider, and provider names never
  // reach a customer-facing surface in this product. Before this, a catalog
  // holding no Hive chat alias returned whatever sorted first, which is an
  // upstream id by definition of having got that far.
  it("never names an upstream model id, whatever the catalog holds", () => {
    const upstreamOnly: CatalogModel[] = [
      model("openai/gpt-4o-mini", "fixed", ["stable", "chat"]),
      model("groq/llama-3.3-70b", "fixed", ["stable", "chat"]),
    ];
    expect(pickQuickstartAlias(upstreamOnly)).toBe("hive-default");
  });
});
