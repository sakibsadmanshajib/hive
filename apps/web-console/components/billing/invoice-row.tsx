// Phase 14 FIX-14-27 — workspace invoice row (BDT-only).
//
// Pure server component — no client interactivity. Renders the period range,
// total in taka, and a download link to the API proxy at
// /api/invoices/{id}/pdf which redirects to the signed Supabase Storage URL.

import Link from "next/link";

import type { InvoiceRecord } from "@/lib/control-plane/client";

interface InvoiceRowProps {
  invoice: InvoiceRecord;
}

function subunitsToTakaDisplay(subunits: number): string {
  if (!Number.isFinite(subunits) || subunits < 0) return "৳0.00";
  const integer = Math.floor(subunits / 100);
  const fraction = subunits % 100;
  // Bangla taka glyph (৳) — regulatory rule: BDT only, no $/USD anywhere.
  return `৳${integer.toLocaleString("en-BD")}.${String(fraction).padStart(2, "0")}`;
}

export function InvoiceRow({ invoice }: InvoiceRowProps) {
  return (
    <tr className="border-b border-[var(--color-border)]">
      <td className="px-3 py-2 text-sm text-[var(--color-ink)]">
        {invoice.period_start} → {invoice.period_end}
      </td>
      <td className="px-3 py-2 text-sm tabular-nums text-[var(--color-ink)]">
        {subunitsToTakaDisplay(invoice.total_bdt_subunits)}
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
