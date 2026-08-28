"use client";

import { AreaChart, Area, ResponsiveContainer } from "recharts";

interface SparklineProps {
  /**
   * One point per bucket, oldest first. A null entry means that bucket had
   * nothing to measure, and it draws as a gap rather than as a measured
   * zero: cacheHitSparkline is null for any bucket with no prompt tokens,
   * and rendering that as 0% would claim a cache hit rate over a slice of
   * time the sample never covered.
   */
  values: ReadonlyArray<number | null>;
  /**
   * CSS color value. Defaults to the accent theme token so the chart tracks
   * light/dark mode like every other new element in this PR (DeltaRow,
   * CacheSplitBar); a hardcoded hex here was the one thing in this diff that
   * didn't.
   */
  color?: string;
  /**
   * What this trend covers, read by screen readers via aria-label on the
   * chart's own wrapping element. Required, not optional: a chart with no
   * accessible name and no visible caption tells a screen-reader user
   * nothing happened here at all, and a sighted user that a number moved
   * without saying over what. The caller supplies the exact sample size and
   * truncation state (see deriveOverviewTiles' sparklineCaption).
   */
  label: string;
}

/**
 * Compact trend line for a summary tile, no axes or tooltip. The caller
 * decides whether a sparkline exists to render at all: this component never
 * fabricates a flat line to fill space when there is no sample behind it,
 * it only draws the values it is handed.
 */
export function Sparkline({
  values,
  color = "var(--color-accent)",
  label,
}: SparklineProps) {
  // Nulls travel through untouched. Recharts renders no point for a null
  // dataKey value, and connectNulls={false} below keeps the line from
  // bridging the gap, which is the honest shape for a bucket with no
  // sample behind it.
  const data = values.map((value, index) => ({ index, value }));

  return (
    <div role="img" aria-label={label}>
      <ResponsiveContainer width={72} height={28}>
        <AreaChart data={data} margin={{ top: 2, right: 1, left: 1, bottom: 0 }}>
          <Area
            type="monotone"
            dataKey="value"
            stroke={color}
            fill={color}
            fillOpacity={0.15}
            strokeWidth={1.5}
            dot={false}
            connectNulls={false}
            isAnimationActive={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
