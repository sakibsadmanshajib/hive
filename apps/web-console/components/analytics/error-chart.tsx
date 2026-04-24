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

interface ErrorChartProps {
  data: ErrorSummaryRow[];
}

export function ErrorChart({ data }: ErrorChartProps) {
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
          <Bar dataKey="error_count" fill="#ef4444" name="Errors" />
          <Bar dataKey="total_requests" fill="#d1d5db" name="Total requests" />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
