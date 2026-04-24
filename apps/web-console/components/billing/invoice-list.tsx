import type { Invoice } from "@/lib/control-plane/client";

interface InvoiceListProps {
  invoices: Invoice[];
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

function statusLabel(status: string): { label: string; color: string } {
  switch (status) {
    case "completed":
    case "paid":
      return { label: "Paid", color: "#10b981" };
    case "pending":
      return { label: "Pending", color: "#f59e0b" };
    case "cancelled":
    case "failed":
      return { label: "Cancelled", color: "#dc2626" };
    default:
      return { label: status, color: "#6b7280" };
  }
}

export function InvoiceList({ invoices }: InvoiceListProps) {
  if (invoices.length === 0) {
    return (
      <p style={{ margin: 0, color: "#6b7280" }}>
        No invoices yet. Invoices appear after each completed purchase.
      </p>
    );
  }

  return (
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
            Invoice #
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
            Amount
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
            Rail
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
            Status
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
            Action
          </th>
        </tr>
      </thead>
      <tbody>
        {invoices.map((invoice) => {
          const { label: statusText, color: statusColor } = statusLabel(invoice.status);
          const amountDisplay = new Intl.NumberFormat("en-US", {
            style: "currency",
            currency: invoice.local_currency || "USD",
            minimumFractionDigits: 2,
          }).format(invoice.amount_local / 100);

          return (
            <tr key={invoice.id}>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                  fontFamily: "monospace",
                }}
              >
                {invoice.invoice_number || invoice.id.slice(0, 8)}
              </td>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                  color: "#6b7280",
                }}
              >
                {formatDate(invoice.created_at)}
              </td>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                }}
              >
                {invoice.credits.toLocaleString()}
              </td>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                }}
              >
                {amountDisplay}
              </td>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                  color: "#6b7280",
                }}
              >
                {invoice.rail}
              </td>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                  color: statusColor,
                  fontWeight: 700,
                }}
              >
                {statusText}
              </td>
              <td
                style={{
                  padding: "0.5rem 0.75rem",
                  borderBottom: "1px solid #f3f4f6",
                  fontSize: "1rem",
                }}
              >
                <a
                  href={`/console/billing/${invoice.id}/download`}
                  style={{ color: "#1d4ed8", textDecoration: "none", fontSize: "0.875rem" }}
                >
                  Download PDF
                </a>
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
