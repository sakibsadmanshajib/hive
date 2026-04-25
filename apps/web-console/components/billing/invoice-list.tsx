import type { Invoice } from "@/lib/control-plane/client";
import { Badge } from "@/components/ui/badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { formatCredits, formatShortDate } from "@/lib/format/credits";
import { InvoiceDownloadButton } from "./invoice-download-button";

interface InvoiceListProps {
  invoices: Invoice[];
}

type ToneName = "success" | "warning" | "danger" | "neutral";

function statusBadge(status: string): { label: string; tone: ToneName } {
  switch (status) {
    case "completed":
    case "paid":
      return { label: "Paid", tone: "success" };
    case "pending":
      return { label: "Pending", tone: "warning" };
    case "cancelled":
    case "failed":
      return { label: "Cancelled", tone: "danger" };
    default:
      return { label: status, tone: "neutral" };
  }
}

function formatLocalAmount(amountCents: number, currency: string): string {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: currency || "USD",
    minimumFractionDigits: 2,
  }).format(amountCents / 100);
}

export function InvoiceList({ invoices }: InvoiceListProps) {
  if (invoices.length === 0) {
    return (
      <EmptyState
        title="No invoices yet"
        description="Invoices appear here once a checkout settles. Successful purchases produce a downloadable PDF."
      />
    );
  }

  const columns: Column<Invoice>[] = [
    {
      key: "invoice_number",
      header: "Invoice",
      cell: (row) => (
        <code className="font-mono text-xs text-[var(--color-ink-2)]">
          {row.invoice_number || row.id.slice(0, 8)}
        </code>
      ),
    },
    {
      key: "date",
      header: "Date",
      numeric: true,
      align: "right",
      cell: (row) => formatShortDate(row.created_at),
    },
    {
      key: "credits",
      header: "Credits",
      numeric: true,
      align: "right",
      cell: (row) => formatCredits(row.credits),
    },
    {
      key: "amount",
      header: "Amount",
      numeric: true,
      align: "right",
      cell: (row) => formatLocalAmount(row.amount_local, row.local_currency),
    },
    {
      key: "rail",
      header: "Method",
      cell: (row) => (
        <span className="text-xs text-[var(--color-ink-3)]">{row.rail}</span>
      ),
    },
    {
      key: "status",
      header: "Status",
      cell: (row) => {
        const { label, tone } = statusBadge(row.status);
        return <Badge tone={tone}>{label}</Badge>;
      },
    },
    {
      key: "actions",
      header: <span className="sr-only">Action</span>,
      align: "right",
      cell: (row) => <InvoiceDownloadButton invoice={row} />,
    },
  ];

  return (
    <DataTable<Invoice>
      rows={invoices}
      columns={columns}
      rowKey={(row) => row.id}
    />
  );
}
