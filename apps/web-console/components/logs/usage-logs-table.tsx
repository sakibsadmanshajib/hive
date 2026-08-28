"use client";

import * as React from "react";

import type { LedgerEntry, UsageEventRow } from "@/lib/control-plane/client";
import { parseLedgerEntriesText } from "@/lib/control-plane/ledger-decode";
import { Badge } from "@/components/ui/badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { cn } from "@/lib/cn";
import { formatCredits, formatLatencyMs, formatTokens } from "@/lib/format/credits";
import { formatDateTime } from "@/lib/format/datetime";

interface UsageLogsTableProps {
  rows: UsageEventRow[];
  // api_key_id -> human key name, joined client-side from the keys API. A
  // missing entry falls back to a short key-id suffix so revoked or deleted
  // keys still render an identifiable column value.
  keyNames: Record<string, string>;
}

// Reservation lifecycle labels for the ledger entry types the gateway posts
// against a request_id. Unknown types still render their raw string.
function lifecycleLabel(entryType: string): string {
  switch (entryType) {
    case "reservation_hold":
      return "Hold";
    case "reservation_release":
      return "Release";
    case "usage_charge":
      return "Charge";
    case "refund":
      return "Refund";
    default:
      return entryType;
  }
}

// Cache token counts are rendered as an em-dash when the field is absent,
// never as 0.
//
// The control-plane omits cache_read_tokens and cache_write_tokens from the
// event payload whenever they are zero (handleListEvents in
// apps/control-plane/internal/usage/http.go), so a missing field and a real
// measured zero arrive here as the identical `undefined`. On top of that,
// decision D-056 records that every cache_write_tokens value written before
// PR #1157 deployed is a bug artifact rather than a measured zero, and a
// stored row carries no marker saying which side of that deploy it fell on.
// Printing "0" would assert a verified zero this column cannot verify, so it
// prints the absence instead.
function formatCacheTokens(value: number | undefined): string {
  return value === undefined ? "—" : formatTokens(value);
}

interface LifecycleState {
  loading: boolean;
  entries: LedgerEntry[] | null;
}

const LIFECYCLE_EMPTY: LifecycleState = { loading: false, entries: null };

// No column starts hidden: every column this table has ever shown
// (including the two cache columns console-cache-visibility.test.tsx pins as
// always-visible) stays visible until the viewer opts out through the
// column-controls checklist.
const DEFAULT_HIDDEN_COLUMNS: ReadonlySet<string> = new Set();

export function UsageLogsTable({ rows, keyNames }: UsageLogsTableProps) {
  const [expandedKey, setExpandedKey] = React.useState<string | null>(null);
  const [lifecycle, setLifecycle] =
    React.useState<Record<string, LifecycleState>>({});
  const [hiddenColumns, setHiddenColumns] = React.useState<ReadonlySet<string>>(
    DEFAULT_HIDDEN_COLUMNS,
  );

  const handleToggle = React.useCallback((key: string) => {
    setExpandedKey((current) => (current === key ? null : key));
  }, []);

  const expandedRow = expandedKey
    ? rows.find((r) => r.id === expandedKey) ?? null
    : null;

  // Fetch the reservation lifecycle lazily, once per request_id, when its row
  // is first expanded. The account-scoped ledger endpoint does the scoping;
  // this call only picks which request_id to read. The lifecycle[requestId]
  // guard fires exactly one fetch per request_id; there is deliberately no
  // cleanup that cancels an in-flight read, because every state update this
  // effect makes re-runs it and would abort the very response it awaits.
  React.useEffect(() => {
    if (!expandedRow || !expandedRow.request_id) return;
    const requestId = expandedRow.request_id;
    if (lifecycle[requestId] !== undefined) return;

    setLifecycle((prev) => ({
      ...prev,
      [requestId]: { loading: true, entries: null },
    }));
    fetch(
      `/api/v1/accounts/current/credits/ledger?request_id=${encodeURIComponent(requestId)}&limit=50`
    )
      .then(async (res) => {
        const text = await res.text().catch(() => "");
        if (!res.ok) {
          throw new Error("lifecycle unavailable");
        }
        // Typed decoder, same parse boundary and validation path
        // getLedgerEntries uses; no unvalidated payload reaches the
        // rendered lifecycle list.
        return parseLedgerEntriesText(text);
      })
      .then((entries) => {
        setLifecycle((prev) => ({
          ...prev,
          [requestId]: { loading: false, entries },
        }));
      })
      .catch(() => {
        setLifecycle((prev) => ({
          ...prev,
          [requestId]: { loading: false, entries: [] },
        }));
      });
  }, [expandedRow, lifecycle]);

  // Every candidate column, in display order. label is the plain-text name
  // shown in the column-controls checklist (header can be JSX; label always
  // a string, so the checklist never has to unwrap it).
  const allColumns: ReadonlyArray<Column<UsageEventRow> & { label: string }> = [
    {
      key: "time",
      label: "Time",
      header: "Time",
      cell: (row) => (
        <span className="text-xs text-[var(--color-ink-2)]">
          {formatDateTime(row.created_at)}
        </span>
      ),
    },
    {
      key: "model",
      label: "Model",
      header: "Model",
      cell: (row) => (
        <span className="font-medium text-[var(--color-ink)]">
          {row.model_alias}
        </span>
      ),
    },
    {
      key: "tokens_in",
      label: "Tokens in",
      header: "Tokens in",
      numeric: true,
      align: "right",
      cell: (row) => formatTokens(row.input_tokens),
    },
    {
      key: "tokens_out",
      label: "Tokens out",
      header: "Tokens out",
      numeric: true,
      align: "right",
      cell: (row) => formatTokens(row.output_tokens),
    },
    {
      key: "cache_read",
      label: "Cached in",
      header: "Cached in",
      numeric: true,
      align: "right",
      cell: (row) => formatCacheTokens(row.cache_read_tokens),
    },
    {
      key: "cache_write",
      label: "Cache write",
      header: "Cache write",
      numeric: true,
      align: "right",
      cell: (row) => formatCacheTokens(row.cache_write_tokens),
    },
    {
      key: "credits",
      label: "Credits",
      header: "Credits",
      numeric: true,
      align: "right",
      cell: (row) => (
        <span
          className={cn(
            row.hive_credit_delta < 0 && "text-[var(--color-danger)]",
            row.hive_credit_delta > 0 && "text-[var(--color-success)]"
          )}
        >
          {row.hive_credit_delta > 0 ? "+" : ""}
          {formatCredits(row.hive_credit_delta)}
        </span>
      ),
    },
    {
      key: "latency",
      label: "Latency",
      header: "Latency",
      numeric: true,
      align: "right",
      cell: (row) => formatLatencyMs(row.latency_ms ?? null),
    },
    {
      key: "status",
      label: "Status",
      header: "Status",
      cell: (row) => (
        <Badge tone={row.status === "completed" ? "success" : "neutral"}>
          {row.status}
        </Badge>
      ),
    },
    {
      key: "key",
      label: "API key",
      header: "API key",
      cell: (row) => {
        if (!row.api_key_id) {
          return <span className="text-xs text-[var(--color-ink-3)]">—</span>;
        }
        return (
          <span className="text-xs text-[var(--color-ink-2)]">
            {keyNames[row.api_key_id] ?? `…${row.api_key_id.slice(-6)}`}
          </span>
        );
      },
    },
  ];

  const columns = allColumns.filter((col) => !hiddenColumns.has(col.key));

  function toggleColumn(key: string) {
    setHiddenColumns((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        // Never let the toggle empty the table entirely: a header row with
        // zero columns is a broken layout, not a valid preference.
        if (allColumns.length - next.size <= 1) return prev;
        next.add(key);
      }
      return next;
    });
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex justify-end">
        <details className="relative">
          <summary
            className={cn(
              "flex cursor-pointer list-none items-center gap-1.5 rounded-md border border-[var(--color-border)]",
              "px-2.5 py-1.5 text-xs text-[var(--color-ink-2)] hover:bg-[var(--color-surface-inset)]",
            )}
            aria-label="Choose columns"
          >
            Columns
          </summary>
          <div
            className="absolute right-0 z-10 mt-1 w-48 rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] p-2 shadow-md"
            data-testid="column-controls"
          >
            {allColumns.map((col) => (
              <label
                key={col.key}
                className="flex items-center gap-2 rounded px-1.5 py-1 text-xs text-[var(--color-ink-2)] hover:bg-[var(--color-surface-inset)]"
              >
                <input
                  type="checkbox"
                  checked={!hiddenColumns.has(col.key)}
                  onChange={() => toggleColumn(col.key)}
                />
                {col.label}
              </label>
            ))}
          </div>
        </details>
      </div>
      <DataTable<UsageEventRow>
        rows={rows}
        columns={columns}
        rowKey={(row) => row.id}
        expandedKey={expandedKey}
        onRowToggle={handleToggle}
        renderDetail={(row) => (
          <UsageLogDetail
            row={row}
            state={lifecycle[row.request_id] ?? LIFECYCLE_EMPTY}
          />
        )}
        empty="No requests match these filters."
      />
    </div>
  );
}

interface UsageLogDetailProps {
  row: UsageEventRow;
  state: LifecycleState;
}

function UsageLogDetail({ row, state }: UsageLogDetailProps) {
  return (
    <div
      data-testid="log-detail"
      className="grid gap-6 sm:grid-cols-2"
    >
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-xs">
        <dt className="text-[var(--color-ink-3)]">Request ID</dt>
        <dd className="break-all font-mono text-[var(--color-ink)]">
          {row.request_id}
        </dd>

        <dt className="text-[var(--color-ink-3)]">Attempt</dt>
        <dd className="break-all font-mono text-[var(--color-ink)]">
          {row.request_attempt_id}
        </dd>

        <dt className="text-[var(--color-ink-3)]">Endpoint</dt>
        <dd className="font-mono text-[var(--color-ink)]">{row.endpoint}</dd>

        <dt className="text-[var(--color-ink-3)]">Event</dt>
        <dd className="text-[var(--color-ink)]">{row.event_type}</dd>

        <dt className="text-[var(--color-ink-3)]">Latency</dt>
        <dd className="text-[var(--color-ink)]">
          {formatLatencyMs(row.latency_ms ?? null)}
        </dd>

        <dt className="text-[var(--color-ink-3)]">Error</dt>
        <dd className="text-[var(--color-ink)]">
          {row.error_code ? (
            <span className="text-[var(--color-danger)]">
              {row.error_code}
              {row.error_type ? ` (${row.error_type})` : ""}
            </span>
          ) : (
            <span className="text-[var(--color-ink-3)]">None</span>
          )}
        </dd>
      </dl>

      <div className="flex flex-col gap-2">
        <p className="text-2xs font-medium uppercase tracking-wider text-[var(--color-ink-3)]">
          Reservation lifecycle
        </p>
        {state.loading ? (
          <p className="text-xs text-[var(--color-ink-3)]">Loading…</p>
        ) : state.entries && state.entries.length > 0 ? (
          <ul className="flex flex-col gap-1" data-testid="lifecycle-list">
            {state.entries.map((entry) => (
              <li
                key={entry.id}
                className="flex items-center justify-between gap-4 text-xs"
              >
                <span className="text-[var(--color-ink-2)]">
                  {lifecycleLabel(entry.entry_type)}
                </span>
                <span
                  className={cn(
                    "tabular-nums",
                    entry.credits_delta < 0
                      ? "text-[var(--color-danger)]"
                      : "text-[var(--color-success)]"
                  )}
                >
                  {entry.credits_delta > 0 ? "+" : ""}
                  {formatCredits(entry.credits_delta)}
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-xs text-[var(--color-ink-3)]">
            No ledger activity for this request.
          </p>
        )}
      </div>
    </div>
  );
}
