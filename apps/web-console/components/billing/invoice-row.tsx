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

function subunitsToTakaDisplay(subunits: string): string {
  // BigInt math — wire shape arrives as a JSON string (Go `,string` tag) so
  // totals beyond Number.MAX_SAFE_INTEGER (2^53−1) preserve full BIGINT
  // precision. The original Number-based path silently rounded for very
  // large monthly totals; BigInt removes the precision risk entirely.
  let n: bigint;
  try {
    n = BigInt(subunits);
  } catch {
    return "৳0.00";
  }
  if (n < 0n) return "৳0.00";
  const integer = n / 100n;
  const fraction = n % 100n;
  // Bangla taka glyph (৳) — regulatory rule: BDT only, no $/USD anywhere.
  // Format the integer part with locale grouping for values up to
  // Number.MAX_SAFE_INTEGER, fall back to plain digits beyond that.
  const integerDisplay =
    integer <= BigInt(Number.MAX_SAFE_INTEGER)
      ? Number(integer).toLocaleString("en-BD")
      : integer.toString();
  return `৳${integerDisplay}.${fraction.toString().padStart(2, "0")}`;
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
