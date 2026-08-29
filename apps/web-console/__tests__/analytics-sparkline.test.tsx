/**
 * The tile sparkline must not turn an unsampled bucket into a measured zero.
 * cacheHitSparkline (lib/analytics/cache-metrics.ts) is null for every bucket
 * with no prompt tokens behind it, and drawing those at 0% asserts a cache
 * hit rate over a slice of time the bounded sample never covered.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";

interface ChartProps {
  children?: React.ReactNode;
  data?: ReadonlyArray<{ index: number; value: number | null }>;
  connectNulls?: boolean;
}

const chartData: ReadonlyArray<{ index: number; value: number | null }>[] = [];
const areaProps: ChartProps[] = [];

vi.mock("recharts", () => {
  return {
    ResponsiveContainer: function ResponsiveContainer(props: ChartProps) {
      return props.children;
    },
    AreaChart: function AreaChart(props: ChartProps) {
      chartData.push(props.data ?? []);
      return props.children;
    },
    Area: function Area(props: ChartProps) {
      areaProps.push(props);
      return null;
    },
  };
});

import { Sparkline } from "@/components/analytics/sparkline";

describe("Sparkline", () => {
  // The capture arrays are module scoped, so a test asserting on index 0
  // would otherwise read whichever test ran first once a third is added.
  beforeEach(() => {
    chartData.length = 0;
    areaProps.length = 0;
  });

  it("keeps an unsampled bucket null instead of drawing it as a measured zero", () => {
    render(
      <Sparkline values={[null, 0.5, null]} label="Trend across all 3 requests" />,
    );
    const values = chartData[0].map((point) => point.value);
    expect(values).toEqual([null, 0.5, null]);
  });

  it("does not bridge the gap a null bucket leaves", () => {
    render(<Sparkline values={[null, 0.5]} label="Trend across all 2 requests" />);
    expect(areaProps[areaProps.length - 1].connectNulls).toBe(false);
  });
});
