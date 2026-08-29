import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { ModelCatalogTable } from "@/components/catalog/model-catalog-table";
import type { CatalogModel } from "@/lib/control-plane/client";
import { chatModelUrl } from "@/lib/chat-link";

const baseModel: CatalogModel = {
  id: "groq/slash-model",
  display_name: "Llama 3.3 70B",
  summary: "",
  capability_badges: ["chat"],
  pricing: {
    input_price_credits: 59,
    output_price_credits: 59,
    cache_read_price_credits: null,
    cache_write_price_credits: null,
    pricing_mode: "fixed",
  },
  lifecycle: "stable",
};

describe("ModelCatalogTable try-in-chat affordance", () => {
  it("links each catalog row into chat with the model preselected", () => {
    render(<ModelCatalogTable models={[baseModel]} />);
    const link = screen.getByRole("link", { name: /try in chat/i });
    expect(link.getAttribute("href")).toBe(chatModelUrl(baseModel.id));
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
  });

  it("keeps the empty-catalog empty state intact", () => {
    render(<ModelCatalogTable models={[]} />);
    expect(screen.getByText("No models available")).toBeTruthy();
  });
});
