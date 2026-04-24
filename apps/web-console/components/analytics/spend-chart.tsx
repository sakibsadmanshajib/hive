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

interface SpendChartProps {
  data: SpendSummaryRow[];
}

export function SpendChart({ data }: SpendChartProps) {
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
        <BarChart data={data} margin={{ top: 8, right: 16, left: 0, bottom: 8 }}>
          <CartesianGrid strokeDasharray="3 3" />
          <XAxis dataKey="group_key" tick={{ fontSize: 12 }} />
          <YAxis tick={{ fontSize: 12 }} />
          <Tooltip />
          <Legend />
          <Bar dataKey="total_credits" fill="#10b981" name="Credits spent" />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
