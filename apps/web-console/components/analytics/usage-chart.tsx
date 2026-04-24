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

interface UsageChartProps {
  data: UsageSummaryRow[];
}

export function UsageChart({ data }: UsageChartProps) {
  return (
    <div
      style={{
        backgroundColor: "#f9fafb",
        borderRadius: "0.75rem",
        padding: "1rem",
        marginBottom: "1.5rem",
      }}
    >
      <ResponsiveContainer width="100%" height={300}>
        <LineChart data={data} margin={{ top: 8, right: 16, left: 0, bottom: 8 }}>
          <CartesianGrid strokeDasharray="3 3" />
          <XAxis dataKey="group_key" tick={{ fontSize: 12 }} />
          <YAxis tick={{ fontSize: 12 }} />
          <Tooltip />
          <Legend />
          <Line
            type="monotone"
            dataKey="total_input_tokens"
            stroke="#6366f1"
            name="Input tokens"
            dot={false}
          />
          <Line
            type="monotone"
            dataKey="total_output_tokens"
            stroke="#8b5cf6"
            name="Output tokens"
            dot={false}
          />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}
