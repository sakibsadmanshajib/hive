/**
 * Cache visibility across the console: the two catalog price columns, the two
 * request-log token columns, the CSV export, and the derivations behind the
 * analytics cache tiles.
 *
 * Every assertion here exists to catch one specific way this feature could
 * lie: a null price rendered as free, an unmeasured token count rendered as a
 * verified zero, an unknown price winning a cheapest-first sort, or a cache
 * hit rate computed against the wrong denominator.
 */
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

import type { CatalogModel, UsageEventRow } from "@/lib/control-plane/client";
import {
  deriveBlendedCreditsPerMillion,
  deriveCacheHitRate,
} from "@/lib/analytics/cache-metrics";
import { ModelCatalogBrowser } from "@/components/catalog/model-catalog-browser";
import { ModelCatalogTable } from "@/components/catalog/model-catalog-table";
import { UsageLogsCsv } from "@/components/logs/usage-logs-csv";
import { UsageLogsTable } from "@/components/logs/usage-logs-table";
import { formatPercent } from "@/lib/format/credits";

function model(overrides: Partial<CatalogModel> = {}): CatalogModel {
  return {
    id: "hive-default",
    display_name: "Hive Default",
    summary: "Balanced default chat model.",
    capability_badges: ["chat"],
    pricing: {
      input_price_credits: 12_000_000,
      output_price_credits: 36_000_000,
      cache_read_price_credits: 1_200_000,
      cache_write_price_credits: 15_000_000,
      pricing_mode: "fixed",
    },
    lifecycle: "stable",
    ...overrides,
  };
}

function event(overrides: Partial<UsageEventRow> = {}): UsageEventRow {
  return {
    id: "e1",
    request_id: "r1",
    request_attempt_id: "a1",
    event_type: "completed",
    endpoint: "/v1/chat/completions",
    model_alias: "hive-default",
    status: "completed",
    input_tokens: 1000,
    output_tokens: 100,
    hive_credit_delta: -500,
    customer_tags: {},
    created_at: "2026-08-25T10:00:00Z",
    ...overrides,
  };
}

describe("deriveCacheHitRate", () => {
  it("divides cache reads by total prompt tokens on the inclusive shape", () => {
    // input_tokens already counts the cached subset, which is the convention
    // every dispatch path in the product reaches today. 800 of 1000 prompt
    // tokens came from cache.
    const result = deriveCacheHitRate([
      event({ input_tokens: 1000, cache_read_tokens: 800 }),
    ]);

    expect(result.promptTokens).toBe(1000);
    expect(result.cachedTokens).toBe(800);
    expect(result.rate).toBe(0.8);
  });

  it("adds the cache components back when the row came from the exclusive shape", () => {
    // Anthropic's native convention reports prompt_tokens with both cache
    // components already removed, so cache read alone exceeds input_tokens.
    // Treating input_tokens as the denominator there would report 400%.
    const result = deriveCacheHitRate([
      event({
        input_tokens: 200,
        cache_read_tokens: 800,
        cache_write_tokens: 0,
      }),
    ]);

    expect(result.promptTokens).toBe(1000);
    expect(result.rate).toBe(0.8);
  });

  it("never reports a rate above 100 percent", () => {
    const result = deriveCacheHitRate([
      event({ input_tokens: 1, cache_read_tokens: 5_000 }),
      event({ input_tokens: 10, cache_read_tokens: 9 }),
    ]);

    expect(result.rate).not.toBeNull();
    expect(result.rate!).toBeLessThanOrEqual(1);
  });

  it("returns a null rate rather than zero on an empty sample", () => {
    // A zero here would claim a measured zero-percent hit rate over a window
    // in which nothing was measured at all.
    const result = deriveCacheHitRate([]);

    expect(result.rate).toBeNull();
    expect(result.sampleSize).toBe(0);
    expect(formatPercent(result.rate)).toBe("—");
  });

  it("counts a row with no cache fields as fully uncached", () => {
    const result = deriveCacheHitRate([
      event({ input_tokens: 500 }),
      event({ input_tokens: 500, cache_read_tokens: 500 }),
    ]);

    expect(result.rate).toBe(0.5);
  });

  it("excludes cache writes from the numerator", () => {
    // D-056: a pre-#1157 cache_write_tokens value is a bug artifact, not a
    // measured quantity, so it must not move the hit rate.
    const withWrite = deriveCacheHitRate([
      event({
        input_tokens: 1000,
        cache_read_tokens: 400,
        cache_write_tokens: 300,
      }),
    ]);

    expect(withWrite.cachedTokens).toBe(400);
    expect(withWrite.rate).toBe(0.4);
  });
});

describe("deriveBlendedCreditsPerMillion", () => {
  it("restates credits per token as credits per million tokens", () => {
    expect(deriveBlendedCreditsPerMillion(2_000, 1_000)).toBe(2_000_000);
  });

  it("returns null on a zero-token window instead of zero or Infinity", () => {
    expect(deriveBlendedCreditsPerMillion(0, 0)).toBeNull();
    expect(deriveBlendedCreditsPerMillion(500, 0)).toBeNull();
  });
});

describe("model catalog cache pricing columns", () => {
  it("renders both cache prices when the alias publishes them", () => {
    render(<ModelCatalogTable models={[model()]} />);

    expect(screen.getByText("Cache read / 1M")).toBeDefined();
    expect(screen.getByText("Cache write / 1M")).toBeDefined();
    expect(screen.getByText("1,200,000")).toBeDefined();
    expect(screen.getByText("15,000,000")).toBeDefined();
  });

  it("renders a dash, never a zero, when a fixed-price alias publishes no cache rate", () => {
    const { container } = render(
      <ModelCatalogTable
        models={[
          model({
            pricing: {
              input_price_credits: 12_000_000,
              output_price_credits: 36_000_000,
              cache_read_price_credits: null,
              cache_write_price_credits: null,
              pricing_mode: "fixed",
            },
          }),
        ]}
      />,
    );

    const cells = Array.from(container.querySelectorAll("td")).map(
      (cell) => cell.textContent,
    );
    expect(cells).toContain("—");
    expect(cells).not.toContain("0");
  });

  it("says Variable for a cache price on an upstream-priced alias", () => {
    render(
      <ModelCatalogTable
        models={[
          model({
            pricing: {
              input_price_credits: null,
              output_price_credits: null,
              cache_read_price_credits: null,
              cache_write_price_credits: null,
              pricing_mode: "upstream_actual",
            },
          }),
        ]}
      />,
    );

    expect(screen.getAllByText("Variable").length).toBe(4);
  });
});

describe("model catalog search, filter and sort", () => {
  const models = [
    model({
      id: "alpha-chat",
      display_name: "Alpha Chat",
      capability_badges: ["chat"],
      pricing: {
        input_price_credits: 9_000_000,
        output_price_credits: 1_000_000,
        cache_read_price_credits: 900_000,
        cache_write_price_credits: null,
        pricing_mode: "fixed",
      },
    }),
    model({
      id: "beta-embed",
      display_name: "Beta Embed",
      capability_badges: ["embeddings"],
      pricing: {
        input_price_credits: 1_000_000,
        output_price_credits: 2_000_000,
        cache_read_price_credits: null,
        cache_write_price_credits: null,
        pricing_mode: "fixed",
      },
    }),
    model({
      id: "gamma-variable",
      display_name: "Gamma Variable",
      capability_badges: ["chat"],
      pricing: {
        input_price_credits: null,
        output_price_credits: null,
        cache_read_price_credits: null,
        cache_write_price_credits: null,
        pricing_mode: "upstream_actual",
      },
    }),
  ];

  function visibleAliases(container: HTMLElement): string[] {
    return Array.from(container.querySelectorAll("tbody code")).map(
      (node) => node.textContent ?? "",
    );
  }

  it("filters rows by a search term over alias, name and summary", () => {
    const { container } = render(<ModelCatalogBrowser models={models} />);

    fireEvent.change(screen.getByLabelText("Search models"), {
      target: { value: "beta" },
    });

    expect(visibleAliases(container)).toEqual(["beta-embed"]);
    expect(screen.getByTestId("catalog-result-count").textContent).toBe(
      "1 of 3 models",
    );
  });

  it("filters rows by capability", () => {
    const { container } = render(<ModelCatalogBrowser models={models} />);

    fireEvent.change(screen.getByLabelText("Capability"), {
      target: { value: "embeddings" },
    });

    expect(visibleAliases(container)).toEqual(["beta-embed"]);
  });

  it("sorts an unpriced alias last in a cheapest-first sort, never first", () => {
    const { container } = render(<ModelCatalogBrowser models={models} />);

    fireEvent.change(screen.getByLabelText("Sort"), {
      target: { value: "input_asc" },
    });

    expect(visibleAliases(container)).toEqual([
      "beta-embed",
      "alpha-chat",
      "gamma-variable",
    ]);
  });

  it("keeps the unpriced alias last in the descending sort too", () => {
    const { container } = render(<ModelCatalogBrowser models={models} />);

    fireEvent.change(screen.getByLabelText("Sort"), {
      target: { value: "input_desc" },
    });

    expect(visibleAliases(container).at(-1)).toBe("gamma-variable");
  });

  it("distinguishes an empty filter result from an empty catalog", () => {
    render(<ModelCatalogBrowser models={models} />);

    fireEvent.change(screen.getByLabelText("Search models"), {
      target: { value: "nothing-matches-this" },
    });

    expect(screen.getByText("No models match these filters")).toBeDefined();
    expect(screen.queryByText("No models available")).toBeNull();
  });
});

describe("request log cache token columns", () => {
  it("renders cache read and cache write counts when present", () => {
    render(
      <UsageLogsTable
        rows={[
          event({ cache_read_tokens: 12_345, cache_write_tokens: 678 }),
        ]}
        keyNames={{}}
      />,
    );

    expect(screen.getByText("Cached in")).toBeDefined();
    expect(screen.getByText("Cache write")).toBeDefined();
    expect(screen.getByText("12,345")).toBeDefined();
    expect(screen.getByText("678")).toBeDefined();
  });

  it("renders an em-dash, not a zero, when the field is absent", () => {
    // The control-plane omits both fields when zero, so the console cannot
    // tell an unmeasured value from a measured zero.
    const { container } = render(
      <UsageLogsTable rows={[event()]} keyNames={{}} />,
    );

    const cells = Array.from(container.querySelectorAll("tbody td")).map(
      (cell) => cell.textContent,
    );
    expect(cells.filter((text) => text === "—").length).toBeGreaterThanOrEqual(
      2,
    );
    expect(cells).not.toContain("0");
  });
});

describe("usage CSV export", () => {
  async function exportedCsv(rows: UsageEventRow[]): Promise<string> {
    let captured = "";
    const createObjectURL = vi
      .spyOn(URL, "createObjectURL")
      .mockImplementation((blob: Blob | MediaSource) => {
        void (blob as Blob)
          .text()
          .then((text) => {
            captured = text;
          })
          .catch(() => {});
        return "blob:mock";
      });
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    render(<UsageLogsCsv rows={rows} keyNames={{}} />);
    fireEvent.click(screen.getByText("Export CSV"));

    // Blob.text() resolves on a microtask; flush before asserting.
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(createObjectURL).toHaveBeenCalled();
    return captured;
  }

  it("carries both cache token columns in the header", async () => {
    const csv = await exportedCsv([event({ cache_read_tokens: 900 })]);

    const [header] = csv.split("\n");
    expect(header.split(",")).toEqual([
      "created_at",
      "request_id",
      "model_alias",
      "status",
      "input_tokens",
      "output_tokens",
      "cache_read_tokens",
      "cache_write_tokens",
      "hive_credit_delta",
      "error_code",
      "api_key",
    ]);
  });

  it("exports an absent cache count as an empty cell rather than a zero", async () => {
    const csv = await exportedCsv([event({ cache_read_tokens: 900 })]);

    const [, row] = csv.split("\n");
    const cells = row.split(",");
    expect(cells[6]).toBe("900");
    expect(cells[7]).toBe("");
  });
});
