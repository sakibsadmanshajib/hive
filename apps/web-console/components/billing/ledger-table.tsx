import type { LedgerEntry } from "@/lib/control-plane/client";
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
      return "Usage Charge";
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

function formatDate(isoString: string): string {
  try {
    return new Date(isoString).toLocaleDateString("en-US", {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  } catch {
    return isoString;
  }
}

type FilterOption = { label: string; value: string | null };

const FILTER_OPTIONS: FilterOption[] = [
  { label: "All", value: null },
  { label: "Purchases", value: "grant" },
  { label: "Charges", value: "usage_charge" },
  { label: "Refunds", value: "refund" },
  { label: "Adjustments", value: "adjustment" },
];

function buildTabUrl(tab: string, type: string | null, cursor: string | null): string {
  const params = new URLSearchParams();
  params.set("tab", tab);
  if (type) {
    params.set("type", type);
  }
  if (cursor) {
    params.set("cursor", cursor);
  }
  return `/console/billing?${params.toString()}`;
}

export function LedgerTable({ entries, nextCursor, currentType, currentCursor }: LedgerTableProps) {
  return (
    <div style={{ display: "grid", gap: "1rem" }}>
      {/* Type filters */}
      <div style={{ display: "flex", gap: "0.5rem", flexWrap: "wrap" }}>
        {FILTER_OPTIONS.map((option) => {
          const isActive = currentType === option.value;
          return (
            <a
              key={option.label}
              href={buildTabUrl("ledger", option.value, null)}
              style={{
                padding: "0.375rem 0.75rem",
                borderRadius: "0.375rem",
                textDecoration: "none",
                fontSize: "0.875rem",
                fontWeight: isActive ? 700 : 400,
                background: isActive ? "#eff6ff" : "#f9fafb",
                color: isActive ? "#1d4ed8" : "#4b5563",
                border: `1px solid ${isActive ? "#93c5fd" : "#e5e7eb"}`,
              }}
            >
              {option.label}
            </a>
          );
        })}
        <LedgerCsvExport entries={entries} />
      </div>

      {entries.length === 0 ? (
        <p style={{ margin: 0, color: "#6b7280" }}>No transactions found.</p>
      ) : (
        <table style={{ width: "100%", borderCollapse: "collapse" }}>
          <thead>
            <tr>
              <th
                style={{
                  padding: "0.5rem 0.75rem",
                  textAlign: "left",
                  fontWeight: 700,
                  fontSize: "0.875rem",
                  borderBottom: "1px solid #e5e7eb",
                  color: "#4b5563",
                }}
              >
                Type
              </th>
              <th
                style={{
                  padding: "0.5rem 0.75rem",
                  textAlign: "left",
                  fontWeight: 700,
                  fontSize: "0.875rem",
                  borderBottom: "1px solid #e5e7eb",
                  color: "#4b5563",
                }}
              >
                Credits
              </th>
              <th
                style={{
                  padding: "0.5rem 0.75rem",
                  textAlign: "left",
                  fontWeight: 700,
                  fontSize: "0.875rem",
                  borderBottom: "1px solid #e5e7eb",
                  color: "#4b5563",
                }}
              >
                Description
              </th>
              <th
                style={{
                  padding: "0.5rem 0.75rem",
                  textAlign: "left",
                  fontWeight: 700,
                  fontSize: "0.875rem",
                  borderBottom: "1px solid #e5e7eb",
                  color: "#4b5563",
                }}
              >
                Date
              </th>
            </tr>
          </thead>
          <tbody>
            {entries.map((entry) => {
              const description =
                typeof entry.metadata?.description === "string"
                  ? entry.metadata.description
                  : "";
              return (
                <tr key={entry.id}>
                  <td
                    style={{
                      padding: "0.5rem 0.75rem",
                      borderBottom: "1px solid #f3f4f6",
                      fontSize: "1rem",
                    }}
                  >
                    {entryTypeLabel(entry.entry_type)}
                  </td>
                  <td
                    style={{
                      padding: "0.5rem 0.75rem",
                      borderBottom: "1px solid #f3f4f6",
                      fontSize: "1rem",
                      color: entry.credits_delta >= 0 ? "#10b981" : "#dc2626",
                    }}
                  >
                    {entry.credits_delta >= 0 ? "+" : ""}
                    {entry.credits_delta.toLocaleString()}
                  </td>
                  <td
                    style={{
                      padding: "0.5rem 0.75rem",
                      borderBottom: "1px solid #f3f4f6",
                      fontSize: "1rem",
                      color: "#6b7280",
                    }}
                  >
                    {description}
                  </td>
                  <td
                    style={{
                      padding: "0.5rem 0.75rem",
                      borderBottom: "1px solid #f3f4f6",
                      fontSize: "1rem",
                      color: "#6b7280",
                    }}
                  >
                    {formatDate(entry.created_at)}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}

      {/* Pagination */}
      <div style={{ display: "flex", gap: "1rem" }}>
        {currentCursor && (
          <a
            href={buildTabUrl("ledger", currentType, null)}
            style={{ color: "#1d4ed8", textDecoration: "none", fontSize: "0.875rem" }}
          >
            Previous
          </a>
        )}
        {nextCursor && (
          <a
            href={buildTabUrl("ledger", currentType, nextCursor)}
            style={{ color: "#1d4ed8", textDecoration: "none", fontSize: "0.875rem" }}
          >
            Next
          </a>
        )}
      </div>
    </div>
  );
}
