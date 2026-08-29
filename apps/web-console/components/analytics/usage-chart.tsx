"use client";

import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
} from "recharts";
import type { UsageSummaryRow } from "@/lib/control-plane/client";
import {
  CHART_HEIGHT,
  CHART_PANEL_CLASS,
  chartAxis,
  chartGridStroke,
  chartLegend,
  chartSeries,
  chartTooltip,
} from "@/components/analytics/chart-theme";

interface UsageChartProps {
  data: UsageSummaryRow[];
}

export function UsageChart({ data }: UsageChartProps) {
  return (
    <div className={CHART_PANEL_CLASS}>
      <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
        <LineChart data={data} margin={{ top: 8, right: 16, left: 0, bottom: 8 }}>
          <CartesianGrid strokeDasharray="3 3" stroke={chartGridStroke} />
          <XAxis dataKey="group_key" {...chartAxis} />
          <YAxis {...chartAxis} />
          <Tooltip {...chartTooltip} />
          <Legend {...chartLegend} />
          <Line
            type="monotone"
            dataKey="total_input_tokens"
            stroke={chartSeries.inputTokens}
            name="Input tokens"
            dot={false}
          />
          <Line
            type="monotone"
            dataKey="total_output_tokens"
            stroke={chartSeries.outputTokens}
            name="Output tokens"
            dot={false}
          />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}
