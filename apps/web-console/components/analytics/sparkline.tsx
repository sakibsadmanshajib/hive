"use client";

import { AreaChart, Area, ResponsiveContainer } from "recharts";

interface SparklineProps {
  /** One point per bucket, oldest first. Absent entries render as a gap of 0. */
  values: ReadonlyArray<number | null>;
  color?: string;
}

/**
 * Compact trend line for a summary tile, no axes or tooltip. The caller
 * decides whether a sparkline exists to render at all: this component never
 * fabricates a flat line to fill space when there is no sample behind it
 * (see the analytics page's EVENT_SAMPLE_WINDOWS gate), it only draws the
 * values it is handed.
 */
export function Sparkline({ values, color = "#6366f1" }: SparklineProps) {
  const data = values.map((value, index) => ({
    index,
    value: value ?? 0,
  }));

  return (
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
          isAnimationActive={false}
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}
