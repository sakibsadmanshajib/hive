/**
 * The 2026-08-25 console parity rescore flagged the model detail page as
 * rendering the three catalog price states on the model detail page
 * inconsistently with the catalog list. The lib
 * (lib/format/model-pricing.ts) pins the invariant: on a cache dimension a
 * null rate on a fixed alias means "no such rate" (a dash, matching the
 * upstream model pages this surface mirrors), while on input/output a null
 * rate on a fixed alias is a broken lookup ("Unknown"). The detail page was
 * routing its cache tile and cache table rows through formatModelPrice, so
 * the same alias showed a dash in the catalog list and "Unknown" on its own
 * detail page.
 *
 * These tests render the real ModelDetail component and pin all three price
 * states on both of its surfaces (the header tiles and the pricing table),
 * so neither screen can drift from the other again.
 */
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { ModelDetail } from "@/components/catalog/model-detail";
import type { CatalogModel } from "@/lib/control-plane/client";
import { CURRENCY_MARK } from "@/tests/support/currency-mark";

function fixture(overrides: Partial<CatalogModel["pricing"]> = {}): CatalogModel {
  return {
    id: "hive-deepseek-v4-flash",
    display_name: "DeepSeek V4 Flash",
    summary: "",
    capability_badges: ["chat"],
    lifecycle: "stable",
    pricing: {
      input_price_credits: 89_460_000,
      output_price_credits: 178_920_000,
      cache_read_price_credits: 2_982_000,
      cache_write_price_credits: 0,
      pricing_mode: "fixed",
      ...overrides,
    },
  };
}

function renderDetail(model: CatalogModel) {
  return render(
    <ModelDetail
      model={model}
      usage={null}
      usageUnavailable={false}
      usageWindowLabel="last 24 hours"
    />,
  );
}

describe("ModelDetail price states", () => {
  it("shows a real cache rate in credits, and in no other unit", () => {
    const { container } = renderDetail(fixture());
    expect(screen.getAllByText("2,982,000").length).toBeGreaterThan(0);
    // Issue #1694: this page printed the dollar figure and the credit integer
    // for the same rate side by side, which is a conversion table. One unit
    // now, and no currency anywhere on the page.
    expect(container.textContent ?? "").not.toMatch(CURRENCY_MARK);
  });

  it("renders a published zero as 0, never Unknown", () => {
    // hive-default publishes cache_write_price_credits = 0; zero is a
    // decision, not an absence.
    renderDetail(fixture({ cache_write_price_credits: 0 }));
    expect(screen.queryAllByText("Unknown")).toHaveLength(0);
    expect(screen.getAllByText("0").length).toBeGreaterThan(0);
  });

  it("never shows Unknown for a missing cache rate on a fixed alias", () => {
    // The catalog list renders this exact fixture's cache cells as a dash
    // (formatCachePrice); the detail page must agree with it.
    renderDetail(fixture({ cache_read_price_credits: null, cache_write_price_credits: null }));
    expect(screen.queryAllByText("Unknown")).toHaveLength(0);
    expect(screen.getAllByText("—").length).toBeGreaterThanOrEqual(2);
  });

  it("still calls a broken lookup Unknown on input/output dimensions", () => {
    // The dash rule is cache-only. A fixed alias whose INPUT rate failed to
    // decode must keep saying Unknown, not borrow the cache dash.
    renderDetail(fixture({ input_price_credits: null }));
    expect(screen.getAllByText("Unknown").length).toBeGreaterThanOrEqual(1);
  });

  it("calls every missing rate Variable on an upstream_actual alias", () => {
    renderDetail(
      fixture({
        input_price_credits: null,
        output_price_credits: null,
        cache_read_price_credits: null,
        cache_write_price_credits: null,
        pricing_mode: "upstream_actual",
      }),
    );
    expect(screen.queryAllByText("Unknown")).toHaveLength(0);
    expect(screen.getAllByText("Variable").length).toBeGreaterThanOrEqual(4);
  });
});
