import type { BalanceSummary, LedgerEntry } from "@/lib/control-plane/client";

interface BillingOverviewProps {
  balance: BalanceSummary;
  recentEntries: LedgerEntry[];
  accountCountryCode: string;
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
    });
  } catch {
    return isoString;
  }
}

export function BillingOverview({ balance, recentEntries, accountCountryCode }: BillingOverviewProps) {
  const recent = recentEntries.slice(0, 5);

  return (
    <div style={{ display: "grid", gap: "1.5rem" }}>
      {/* Balance card */}
      <div
        style={{
          border: "1px solid #d1d5db",
          borderRadius: "0.75rem",
          padding: "1rem",
          display: "grid",
          gap: "1rem",
        }}
      >
        <div style={{ display: "grid", gap: "0.25rem" }}>
          <span style={{ fontSize: "0.875rem", color: "#4b5563", fontWeight: 700 }}>
            Current balance
          </span>
          <span style={{ fontSize: "1.5rem", fontWeight: 700, color: "#1f2937" }}>
            {balance.available_credits.toLocaleString()} Hive Credits
          </span>
          {balance.reserved_credits > 0 && (
            <span style={{ fontSize: "0.875rem", color: "#6b7280" }}>
              {balance.reserved_credits.toLocaleString()} credits reserved
            </span>
          )}
        </div>

        <div style={{ display: "flex", gap: "1rem", alignItems: "center" }}>
          {/* Buy Credits button rendered as a form trigger — client-side modal opened via data attribute */}
          <a
            href="/console/billing?action=buy"
            style={{
              display: "inline-block",
              background: "#1d4ed8",
              color: "#ffffff",
              padding: "0.5rem 1rem",
              borderRadius: "0.375rem",
              textDecoration: "none",
              fontWeight: 700,
              fontSize: "1rem",
            }}
          >
            Buy Credits
          </a>
          <a
            href="/console/settings/billing"
            style={{ color: "#1d4ed8", fontSize: "0.875rem" }}
          >
            Tax profile settings
          </a>
        </div>
      </div>

      {/* Recent transactions */}
      <div
        style={{
          border: "1px solid #d1d5db",
          borderRadius: "0.75rem",
          padding: "1rem",
          display: "grid",
          gap: "1rem",
        }}
      >
        <h2 style={{ margin: 0, fontSize: "1.25rem", fontWeight: 700 }}>
          Recent transactions
        </h2>

        {recent.length === 0 ? (
          <div style={{ display: "grid", gap: "0.5rem", textAlign: "center", padding: "2rem 0" }}>
            <p style={{ margin: 0, fontWeight: 700, color: "#1f2937" }}>No transactions yet</p>
            <p style={{ margin: 0, color: "#6b7280" }}>
              Your credit transactions will appear here after your first top-up.
            </p>
            <a
              href="/console/billing?action=buy"
              style={{
                display: "inline-block",
                width: "fit-content",
                margin: "0 auto",
                background: "#1d4ed8",
                color: "#ffffff",
                padding: "0.5rem 1rem",
                borderRadius: "0.375rem",
                textDecoration: "none",
                fontWeight: 700,
              }}
            >
              Buy Credits
            </a>
          </div>
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
                  Date
                </th>
              </tr>
            </thead>
            <tbody>
              {recent.map((entry) => (
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
                    {formatDate(entry.created_at)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}

        {recent.length > 0 && (
          <a
            href="/console/billing?tab=ledger"
            style={{ color: "#1d4ed8", fontSize: "0.875rem", textDecoration: "none" }}
          >
            View full ledger history
          </a>
        )}
      </div>
    </div>
  );
}
