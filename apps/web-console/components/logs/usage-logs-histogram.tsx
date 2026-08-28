"use client";

import {
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import type { UsageEventRow } from "@/lib/control-plane/client";

// Fixed-width buckets, upper bound inclusive, chosen to match the shape a
// gateway's own latency distribution actually takes: most requests land
// under a couple of seconds, with a long tail worth seeing as its own
// bucket rather than compressed into one "everything else" bar.
const BUCKETS: ReadonlyArray<{ label: string; maxMs: number }> = [
  { label: "<100ms", maxMs: 100 },
  { label: "100-500ms", maxMs: 500 },
  { label: "500ms-1s", maxMs: 1000 },
  { label: "1-2s", maxMs: 2000 },
  { label: "2-5s", maxMs: 5000 },
  { label: "5-10s", maxMs: 10000 },
  { label: ">10s", maxMs: Infinity },
];

export interface LatencyBucket {
  label: string;
  count: number;
}

// Exported so the bucketing rule itself is unit-testable without mounting
// recharts (which needs a real layout engine to report non-zero size).
export function bucketLatencies(
  rows: ReadonlyArray<Pick<UsageEventRow, "latency_ms">>,
): LatencyBucket[] {
  const counts = BUCKETS.map(() => 0);
  for (const row of rows) {
    if (row.latency_ms === undefined) continue;
    const idx = BUCKETS.findIndex((bucket) => row.latency_ms! <= bucket.maxMs);
    counts[idx === -1 ? BUCKETS.length - 1 : idx] += 1;
  }
  return BUCKETS.map((bucket, i) => ({ label: bucket.label, count: counts[i] }));
}

interface UsageLogsHistogramProps {
  rows: UsageEventRow[];
}

export function UsageLogsHistogram({ rows }: UsageLogsHistogramProps) {
  const withLatency = rows.filter((row) => row.latency_ms !== undefined);
  const buckets = bucketLatencies(withLatency);

  return (
    <div className="mb-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-4">
      <p className="mb-2 text-2xs font-medium uppercase tracking-wider text-[var(--color-ink-3)]">
        Latency distribution
      </p>
      {withLatency.length === 0 ? (
        <p className="py-8 text-center text-xs text-[var(--color-ink-3)]">
          No completed requests in this page carry a measured latency yet.
        </p>
      ) : (
        <ResponsiveContainer width="100%" height={140}>
          <BarChart data={buckets} margin={{ top: 4, right: 8, left: 0, bottom: 4 }}>
            <CartesianGrid strokeDasharray="3 3" vertical={false} />
            <XAxis dataKey="label" tick={{ fontSize: 11 }} />
            <YAxis allowDecimals={false} tick={{ fontSize: 11 }} width={28} />
            <Tooltip />
            <Bar dataKey="count" name="Requests" fill="var(--color-accent)" />
          </BarChart>
        </ResponsiveContainer>
      )}
    </div>
  );
}
