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
import type { ErrorSummaryRow } from "@/lib/control-plane/client";
import {
  CHART_HEIGHT,
  CHART_PANEL_CLASS,
  chartAxis,
  chartGridStroke,
  chartLegend,
  chartSeries,
  chartTooltip,
} from "@/components/analytics/chart-theme";

interface ErrorChartProps {
  data: ErrorSummaryRow[];
}

export function ErrorChart({ data }: ErrorChartProps) {
  return (
    <div className={CHART_PANEL_CLASS}>
      <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
        <BarChart data={data} margin={{ top: 8, right: 16, left: 0, bottom: 8 }}>
          <CartesianGrid strokeDasharray="3 3" stroke={chartGridStroke} />
          <XAxis dataKey="group_key" {...chartAxis} />
          <YAxis {...chartAxis} />
          <Tooltip {...chartTooltip} />
          <Legend {...chartLegend} />
          <Bar dataKey="error_count" fill={chartSeries.errors} name="Errors" />
          <Bar dataKey="total_requests" fill={chartSeries.totalRequests} name="Total requests" />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
