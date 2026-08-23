"use client";

import * as React from "react";

import type { LedgerEntry, UsageEventRow } from "@/lib/control-plane/client";
import { Badge } from "@/components/ui/badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { cn } from "@/lib/cn";
import { formatCredits } from "@/lib/format/credits";
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

interface LifecycleState {
  loading: boolean;
  entries: LedgerEntry[] | null;
}

const LIFECYCLE_EMPTY: LifecycleState = { loading: false, entries: null };

export function UsageLogsTable({ rows, keyNames }: UsageLogsTableProps) {
  const [expandedKey, setExpandedKey] = React.useState<string | null>(null);
  const [lifecycle, setLifecycle] =
    React.useState<Record<string, LifecycleState>>({});

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
        const body: unknown = await res.json().catch(() => null);
        if (!res.ok || body === null || typeof body !== "object") {
          throw new Error("lifecycle unavailable");
        }
        const entries = (body as { entries?: unknown }).entries;
        return Array.isArray(entries) ? (entries as LedgerEntry[]) : [];
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

  const columns: ReadonlyArray<Column<UsageEventRow>> = [
    {
      key: "time",
      header: "Time",
      cell: (row) => (
        <span className="text-xs text-[var(--color-ink-2)]">
          {formatDateTime(row.created_at)}
        </span>
      ),
    },
    {
      key: "model",
      header: "Model",
      cell: (row) => (
        <span className="font-medium text-[var(--color-ink)]">
          {row.model_alias}
        </span>
      ),
    },
    {
      key: "tokens_in",
      header: "Tokens in",
      numeric: true,
      align: "right",
      cell: (row) => formatCredits(row.input_tokens),
    },
    {
      key: "tokens_out",
      header: "Tokens out",
      numeric: true,
      align: "right",
      cell: (row) => formatCredits(row.output_tokens),
    },
    {
      key: "credits",
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
      key: "status",
      header: "Status",
      cell: (row) => (
        <Badge tone={row.status === "completed" ? "success" : "neutral"}>
          {row.status}
        </Badge>
      ),
    },
    {
      key: "key",
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

  return (
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
