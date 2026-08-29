"use client";

import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
} from "recharts";
import type { SpendSummaryRow } from "@/lib/control-plane/client";
import {
  CHART_HEIGHT,
  CHART_PANEL_CLASS,
  chartAxis,
  chartGridStroke,
  chartLegend,
  chartSeries,
  chartTooltip,
} from "@/components/analytics/chart-theme";

interface SpendChartProps {
  data: SpendSummaryRow[];
}

export function SpendChart({ data }: SpendChartProps) {
  return (
    <div className={CHART_PANEL_CLASS}>
      <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
        <BarChart data={data} margin={{ top: 8, right: 16, left: 0, bottom: 8 }}>
          <CartesianGrid strokeDasharray="3 3" stroke={chartGridStroke} />
          <XAxis dataKey="group_key" {...chartAxis} />
          <YAxis {...chartAxis} />
          <Tooltip {...chartTooltip} />
          <Legend {...chartLegend} />
          <Bar dataKey="total_credits" fill={chartSeries.credits} name="Credits spent" />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
