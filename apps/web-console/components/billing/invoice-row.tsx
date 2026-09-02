// Workspace invoice row.
//
// Pure server component, no client interactivity. Two money columns, and they
// are different units (issue #1681): the consumption quantity is Hive credits,
// the unit the ledger stores and the rest of this console prints, and the
// charged amount is taka. Neither is derived from the other here; the server
// converts once, at a rate it records on the row.

import Link from "next/link";

import type { InvoiceRecord } from "@/lib/control-plane/client";
import { formatCreditCount } from "@/lib/format/credits";
import { formatTakaSubunits } from "@/lib/format/money";

interface InvoiceRowProps {
  invoice: InvoiceRecord;
}

export function InvoiceRow({ invoice }: InvoiceRowProps) {
  return (
    <tr className="border-b border-[var(--color-border)]">
      <td className="px-3 py-2 text-sm text-[var(--color-ink)]">
        {invoice.period_start} → {invoice.period_end}
      </td>
      <td className="metric px-3 py-2 text-sm text-[var(--color-ink)]">
        {formatCreditCount(invoice.total_credits)}
      </td>
      <td className="metric px-3 py-2 text-sm text-[var(--color-ink)]">
        {formatTakaSubunits(invoice.total_bdt_subunits)}
      </td>
      <td className="px-3 py-2 text-sm text-[var(--color-ink-3)]">
        {invoice.line_items.length}{" "}
        {invoice.line_items.length === 1 ? "model" : "models"}
      </td>
      <td className="px-3 py-2 text-sm">
        <Link
          href={`/api/invoices/${invoice.id}/pdf`}
          className="text-[var(--color-link)] hover:underline"
          prefetch={false}
        >
          Download PDF
        </Link>
      </td>
    </tr>
  );
}
