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
  it("links a chat-capable row into chat with the model preselected", () => {
    render(<ModelCatalogTable models={[baseModel]} />);
    const link = screen.getByRole("link", { name: /try in chat/i });
    expect(link.getAttribute("href")).toBe(chatModelUrl(baseModel.id));
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
  });

  it("offers no chat link on a model that cannot serve a chat completion", () => {
    // hive-embedding-default, hive-stt and hive-tts are all in the live
    // catalog and none of them can answer /v1/chat/completions, so the link
    // used to drop a prospect into a chat window whose first send fails
    // (issue #1647).
    const embedding: CatalogModel = {
      ...baseModel,
      id: "hive-embedding-default",
      display_name: "Hive Embedding Default",
      capability_badges: ["stable", "embeddings"],
    };
    const stt: CatalogModel = {
      ...baseModel,
      id: "hive-stt",
      display_name: "Hive Voice STT",
      capability_badges: ["voice", "stt"],
    };
    const tts: CatalogModel = {
      ...baseModel,
      id: "hive-tts",
      display_name: "Hive Voice TTS",
      capability_badges: ["voice", "tts"],
    };

    render(<ModelCatalogTable models={[embedding, stt, tts]} />);

    expect(screen.queryByRole("link", { name: /try in chat/i })).toBeNull();
  });

  it("keeps the chat link on chat-capable rows in the same table", () => {
    const voice: CatalogModel = {
      ...baseModel,
      id: "hive-tts",
      capability_badges: ["voice", "tts"],
    };

    render(<ModelCatalogTable models={[baseModel, voice]} />);

    const links = screen.getAllByRole("link", { name: /try in chat/i });
    expect(links).toHaveLength(1);
    expect(links[0].getAttribute("href")).toBe(chatModelUrl(baseModel.id));
  });

  it("keeps the empty-catalog empty state intact", () => {
    render(<ModelCatalogTable models={[]} />);
    expect(screen.getByText("No models available")).toBeTruthy();
  });
});
