import Link from "next/link";

import type { LedgerEntry } from "@/lib/control-plane/client";
import { buttonVariants } from "@/components/ui/button";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { cn } from "@/lib/cn";
import { formatCredits } from "@/lib/format/credits";
import { LedgerCsvExport } from "./ledger-csv-export";

interface LedgerTableProps {
  entries: LedgerEntry[];
  nextCursor: string | null;
  currentType: string | null;
  currentCursor: string | null;
}

function entryTypeLabel(entryType: string): string {
  switch (entryType) {
    case "grant":
      return "Purchase";
    case "usage_charge":
      return "Usage charge";
    case "refund":
      return "Refund";
    case "adjustment":
      return "Adjustment";
    case "reservation_hold":
      return "Reserved";
    case "reservation_release":
      return "Released";
    default:
      return entryType;
  }
}

function formatDateTime(isoString: string): string {
  const date = new Date(isoString);
  if (Number.isNaN(date.getTime())) {
    return isoString;
  }
  return new Intl.DateTimeFormat("en-US", {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

interface FilterOption {
  label: string;
  value: string | null;
}

const FILTER_OPTIONS: ReadonlyArray<FilterOption> = [
  { label: "All", value: null },
  { label: "Purchases", value: "grant" },
  { label: "Charges", value: "usage_charge" },
  { label: "Refunds", value: "refund" },
  { label: "Adjustments", value: "adjustment" },
];

function buildLedgerUrl(type: string | null, cursor: string | null): string {
  const params = new URLSearchParams();
  params.set("tab", "ledger");
  if (type) params.set("type", type);
  if (cursor) params.set("cursor", cursor);
  return `/console/billing?${params.toString()}`;
}

function readMetadataDescription(metadata: Record<string, unknown>): string {
  const value = metadata.description;
  return typeof value === "string" ? value : "";
}

export function LedgerTable({
  entries,
  nextCursor,
  currentType,
  currentCursor,
}: LedgerTableProps) {
  const columns: Column<LedgerEntry>[] = [
    {
      key: "type",
      header: "Type",
      cell: (row) => entryTypeLabel(row.entry_type),
    },
    {
      key: "credits",
      header: "Credits",
      numeric: true,
      align: "right",
      cell: (row) => (
        <span
          className={
            row.credits_delta >= 0
              ? "text-[var(--color-success)]"
              : "text-[var(--color-danger)]"
          }
        >
          {row.credits_delta >= 0 ? "+" : ""}
          {formatCredits(row.credits_delta)}
        </span>
      ),
    },
    {
      key: "description",
      header: "Description",
      cell: (row) => (
        <span className="text-xs text-[var(--color-ink-3)]">
          {readMetadataDescription(row.metadata)}
        </span>
      ),
    },
    {
      key: "date",
      header: "Date",
      numeric: true,
      align: "right",
      cell: (row) => formatDateTime(row.created_at),
    },
  ];

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        {FILTER_OPTIONS.map((option) => {
          const isActive = currentType === option.value;
          return (
            <Link
              key={option.label}
              href={buildLedgerUrl(option.value, null)}
              className={cn(
                "inline-flex h-8 items-center rounded-md border px-3 text-xs",
                "transition-colors",
                isActive
                  ? "border-[var(--color-border-strong)] bg-[var(--color-surface-inset)] text-[var(--color-ink)]"
                  : "border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-ink-3)] hover:border-[var(--color-border-strong)] hover:text-[var(--color-ink)]",
              )}
            >
              {option.label}
            </Link>
          );
        })}
        <div className="ml-auto">
          <LedgerCsvExport entries={entries} />
        </div>
      </div>

      {entries.length === 0 ? (
        <EmptyState
          title="No transactions"
          description="There are no ledger entries for this filter."
        />
      ) : (
        <DataTable<LedgerEntry>
          rows={entries}
          columns={columns}
          rowKey={(row) => row.id}
        />
      )}

      {(currentCursor || nextCursor) && (
        <div className="flex items-center gap-2">
          {currentCursor ? (
            <Link
              href={buildLedgerUrl(currentType, null)}
              className={buttonVariants({ variant: "secondary", size: "sm" })}
            >
              Reset
            </Link>
          ) : null}
          {nextCursor ? (
            <Link
              href={buildLedgerUrl(currentType, nextCursor)}
              className={buttonVariants({ variant: "secondary", size: "sm" })}
            >
              Next
            </Link>
          ) : null}
        </div>
      )}
    </div>
  );
}
