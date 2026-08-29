import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { ChartCard } from "@/components/analytics/chart-card";

describe("ChartCard empty state", () => {
  it("replaces the chart with an explanation when there are no rows", () => {
    render(
      <ChartCard title="Usage" rows={[]}>
        <div data-testid="chart" />
      </ChartCard>,
    );

    expect(screen.getByText("No activity in this time range yet.")).toBeTruthy();
    expect(screen.queryByTestId("chart")).toBeNull();
  });

  it("renders a caller supplied empty message", () => {
    render(
      <ChartCard title="Spend" rows={[]} emptyMessage="Nothing spent yet.">
        <div data-testid="chart" />
      </ChartCard>,
    );

    expect(screen.getByText("Nothing spent yet.")).toBeTruthy();
  });

  it("renders the chart once there is at least one row", () => {
    render(
      <ChartCard title="Usage" rows={[{ group_key: "2026-08-29" }]}>
        <div data-testid="chart" />
      </ChartCard>,
    );

    expect(screen.getByTestId("chart")).toBeTruthy();
    expect(screen.queryByText("No activity in this time range yet.")).toBeNull();
  });
});
