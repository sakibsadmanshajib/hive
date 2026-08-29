/**
 * Issue #1406: /console/analytics was the one console route that scrolled
 * the page sideways at a phone width. A grid item's default min-width is
 * `auto`, which is its min-content width, so one card whose row will not
 * wrap sized the whole auto track past the container and every sibling card
 * stretched to match. Measured on the deployed console at 375: cards 411
 * wide, main scrollWidth 427.
 *
 * jsdom computes no layout, so what is pinned here is the constraint that
 * removes the min-content contribution. The measured before and after
 * numbers are in the pull request's capture log.
 */
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render } from "@testing-library/react";

import { AnalyticsOverviewSection } from "./analytics-overview-section";
import { deriveOverviewTiles } from "@/lib/analytics/cache-metrics";
import type { UsageSummaryRow } from "@/lib/control-plane/client";

afterEach(() => {
  cleanup();
});

const USAGE: UsageSummaryRow[] = [
  {
    group_key: "hive-auto",
    total_input_tokens: 14_512,
    total_output_tokens: 3_004,
    total_credits_spent: 2_834_000_000,
    request_count: 37,
  },
];

function renderSection() {
  const tiles = deriveOverviewTiles({
    timeWindow: "30d",
    usage: USAGE,
    previousUsage: [],
    cacheSample: [],
    cacheSampleTruncated: false,
    previousCacheSample: [],
    topKeys: {
      spend: [
        { group_key: "unattributed", total_credits: 2_442_294, entry_count: 37 },
      ],
      keys: [],
    },
  });
  return render(
    <AnalyticsOverviewSection tiles={tiles} usageData={USAGE} />,
  );
}

describe("AnalyticsOverviewSection card grids", () => {
  it("stops a card's min-content width from sizing its grid track", () => {
    const { container } = renderSection();
    const root = container.firstElementChild;
    const grids = [...(root?.children ?? [])].filter((el) =>
      el.classList.contains("grid"),
    );

    expect(grids.length).toBe(2);
    for (const grid of grids) {
      expect(grid.className).toContain("[&>*]:min-w-0");
    }
  });
});
