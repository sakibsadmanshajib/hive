import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import type { UsageEventRow } from "@/lib/control-plane/client";
import { bucketLatencies, UsageLogsHistogram } from "./usage-logs-histogram";

function row(latency_ms?: number): Pick<UsageEventRow, "latency_ms"> {
  return { latency_ms };
}

describe("bucketLatencies", () => {
  it("sorts values into their fixed-width bucket", () => {
    const buckets = bucketLatencies([
      row(40), // <100ms
      row(340), // 100-500ms
      row(1800), // 1-2s
      row(1900), // 1-2s
      row(25000), // >10s
    ]);

    const byLabel = Object.fromEntries(buckets.map((b) => [b.label, b.count]));
    expect(byLabel["<100ms"]).toBe(1);
    expect(byLabel["100-500ms"]).toBe(1);
    expect(byLabel["1-2s"]).toBe(2);
    expect(byLabel[">10s"]).toBe(1);
    expect(byLabel["500ms-1s"]).toBe(0);
  });

  it("ignores rows with no latency measurement", () => {
    const buckets = bucketLatencies([row(undefined), row(undefined)]);
    const total = buckets.reduce((sum, b) => sum + b.count, 0);
    expect(total).toBe(0);
  });

  it("excludes a negative latency instead of folding it into the fastest bucket", () => {
    // A negative value is bad data (clock skew), not a fast request.
    // formatLatencyMs already renders it as the em-dash on the table column;
    // the histogram must agree rather than silently count it as <100ms.
    const buckets = bucketLatencies([row(-5), row(40)]);
    const byLabel = Object.fromEntries(buckets.map((b) => [b.label, b.count]));
    expect(byLabel["<100ms"]).toBe(1);
    const total = buckets.reduce((sum, b) => sum + b.count, 0);
    expect(total).toBe(1);
  });
});

describe("UsageLogsHistogram", () => {
  it("shows an honest empty state when no row carries a measured latency", () => {
    render(
      <UsageLogsHistogram
        rows={[{ ...baseRow(), latency_ms: undefined }]}
      />
    );
    expect(
      screen.getByText(
        "No completed requests in this page carry a measured latency yet."
      )
    ).toBeTruthy();
  });

  it("falls back to the empty state when every row's latency is negative", () => {
    render(<UsageLogsHistogram rows={[{ ...baseRow(), latency_ms: -12 }]} />);
    expect(
      screen.getByText(
        "No completed requests in this page carry a measured latency yet."
      )
    ).toBeTruthy();
  });

  it("renders the distribution label once latency data is present", () => {
    render(
      <UsageLogsHistogram rows={[{ ...baseRow(), latency_ms: 340 }]} />
    );
    expect(screen.getByText("Latency distribution")).toBeTruthy();
    expect(
      screen.queryByText(
        "No completed requests in this page carry a measured latency yet."
      )
    ).toBeNull();
  });
});

function baseRow(): UsageEventRow {
  return {
    id: "evt_1",
    request_id: "req_1",
    request_attempt_id: "att_1",
    event_type: "completed",
    endpoint: "/v1/chat/completions",
    model_alias: "hive-fast",
    status: "completed",
    input_tokens: 10,
    output_tokens: 5,
    hive_credit_delta: -1,
    customer_tags: {},
    created_at: "2026-08-22T10:00:00Z",
  };
}
