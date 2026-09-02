// Workspace usage statement row.
//
// Pure server component, no client interactivity. One quantity column, and its
// unit is Hive credits, which is what the ledger stores and what the rest of
// this console prints (issue #1681).
//
// There is deliberately no money column. A usage period is a prepaid draw down
// against credits the customer already bought, so a fiat figure here would read
// as a second bill for consumption already paid for (owner ruling,
// 2026-09-02). It would also disclose something confidential: credits are sold
// at a markup and a subscription grants a credit quantity whose internal value
// is not published, so converting consumed credits back into money at the
// internal peg would publish exactly that figure. The taka amount is still
// stored on the row for audit, and is still repaired by the issue #1682 pass;
// the control-plane simply does not send it to a customer.
import Link from "next/link";

import type { InvoiceRecord } from "@/lib/control-plane/client";
import { formatCreditCount } from "@/lib/format/credits";

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
