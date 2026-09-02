import type { BalanceSummary } from "@/lib/control-plane/client";
import { formatCreditAmount } from "@/lib/format/credits";

/**
 * The account balance, drawn the same way wherever it appears (issue #1332).
 *
 * One denomination, and it is Hive credits: the unit the ledger moves, the
 * invoices count and the per-key caps are set in. No currency figure beside
 * it, per the owner ruling recorded as .wolf/decisions.md D-070 (issue #1694).
 *
 * This component used to render the balance in US dollars and then state the
 * conversion under it, in the words "1,000,000,000 credits per $1.00". That
 * single line published the credit peg, and from the peg every internal figure
 * follows: credits are sold at a markup (D-065) and a subscription grants a
 * credit quantity whose internal value the owner requires stay unpublished, so
 * a customer paying one price could read a different value off their own
 * balance. The line is gone and so is the dollar rendering above it.
 */
export function CreditBalance({ balance }: { balance: BalanceSummary }) {
  return (
    <div className="flex flex-col gap-1">
      <p className="metric text-3xl text-[var(--color-ink)]" data-numeric>
        {formatCreditAmount(balance.available_credits)}
      </p>
      <p className="text-xs text-[var(--color-ink-3)]">
        Posted{" "}
        <span className="metric text-[var(--color-ink-2)]">
          {formatCreditAmount(balance.posted_credits)}
        </span>{" "}
        · Reserved{" "}
        <span className="metric text-[var(--color-ink-2)]">
          {formatCreditAmount(balance.reserved_credits)}
        </span>
      </p>
    </div>
  );
}
