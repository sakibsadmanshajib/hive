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
//
// The first label reads as a closed range because the comparison below is
// inclusive: a request measured at exactly 100ms lands here, not in the next
// bucket. Every other label already reads that way.
const BUCKETS: ReadonlyArray<{ label: string; maxMs: number }> = [
  { label: "0-100ms", maxMs: 100 },
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

type LatencySample = Pick<UsageEventRow, "latency_ms" | "request_attempt_id">;

// A negative latency is not a fast request, it is bad data (clock skew
// between the two timestamps request_attempts derives it from, or a replayed
// event). formatLatencyMs already renders negative as the em-dash rather
// than folding it into the fastest bucket; this predicate is what keeps the
// histogram's "has a measured latency" decision consistent with that, so a
// page whose only latency values are negative falls back to the honest empty
// state instead of silently counting bad data as sub-100ms requests.
function hasMeasuredLatency(
  row: LatencySample,
): row is LatencySample & { latency_ms: number } {
  return row.latency_ms !== undefined && row.latency_ms >= 0;
}

// One request writes several usage events against the same
// request_attempt_id: accounting records reservation_created and the
// settlement event (apps/control-plane/internal/accounting/service.go), and
// edge-api records completed or error
// (apps/edge-api/internal/inference/orchestrator.go). The LEFT JOIN behind
// latency_ms hands every one of those rows the same derived value, so
// counting events would weight this distribution by accounting outcome
// rather than by traffic: a request that settled and then refunded sits in
// its bucket three times while a plain completion sits there twice.
// Collapsing to one sample per attempt is what lets the series keep calling
// itself Requests.
//
// Exported so the bucketing rule itself is unit-testable without mounting
// recharts (which needs a real layout engine to report non-zero size).
export function bucketLatencies(
  rows: ReadonlyArray<LatencySample>,
): LatencyBucket[] {
  // Keying the map on the attempt is the whole deduplication: every event
  // of one attempt carries the same derived latency, so which of them wins
  // the slot does not matter and a first-wins guard here would be a branch
  // no test could ever turn red.
  const byAttempt = new Map<string, number>();
  for (const row of rows) {
    if (!hasMeasuredLatency(row)) continue;
    byAttempt.set(row.request_attempt_id, row.latency_ms);
  }

  // No -1 fallback: the last bucket is unbounded, so findIndex always
  // matches. Give any bucket a finite maxMs and that stops being true.
  const counts = BUCKETS.map(() => 0);
  for (const latency of byAttempt.values()) {
    counts[BUCKETS.findIndex((bucket) => latency <= bucket.maxMs)] += 1;
  }
  return BUCKETS.map((bucket, i) => ({ label: bucket.label, count: counts[i] }));
}

interface UsageLogsHistogramProps {
  rows: UsageEventRow[];
}

export function UsageLogsHistogram({ rows }: UsageLogsHistogramProps) {
  const buckets = bucketLatencies(rows);
  // Totalled off the buckets themselves, so the caption can never
  // disagree with the bars it sits under.
  const measured = buckets.reduce((sum, bucket) => sum + bucket.count, 0);

  return (
    <div className="mb-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-4">
      <p className="mb-2 text-2xs font-medium uppercase tracking-wider text-[var(--color-ink-3)]">
        Latency distribution
      </p>
      {measured === 0 ? (
        <p className="py-8 text-center text-xs text-[var(--color-ink-3)]">
          No completed requests in this page carry a measured latency yet.
        </p>
      ) : (
        <>
          <ResponsiveContainer width="100%" height={140}>
            <BarChart data={buckets} margin={{ top: 4, right: 8, left: 0, bottom: 4 }}>
              <CartesianGrid strokeDasharray="3 3" vertical={false} />
              <XAxis dataKey="label" tick={{ fontSize: 11 }} />
              <YAxis allowDecimals={false} tick={{ fontSize: 11 }} width={28} />
              <Tooltip />
              <Bar dataKey="count" name="Requests" fill="var(--color-accent)" />
            </BarChart>
          </ResponsiveContainer>
          {/* The page fetches one 50-row page of events, so this chart
              describes that page and not the account. Say so, rather
              than letting a 50-row sample read as an account-wide
              distribution. */}
          <p className="mt-2 text-2xs text-[var(--color-ink-3)]">
            {measured} measured {measured === 1 ? "request" : "requests"} on
            this page only, not the whole account.
          </p>
        </>
      )}
    </div>
  );
}
