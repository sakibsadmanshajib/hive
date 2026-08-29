import type { BalanceSummary } from "@/lib/control-plane/client";
import {
  CREDITS_PER_USD,
  formatCredits,
  formatUsdBalanceFromCredits,
} from "@/lib/format/credits";

/**
 * The account balance, drawn the same way wherever it appears (issue #1332).
 *
 * One denomination, chosen once: US dollars, because that is what the rest of
 * this console already puts in front of a customer (the API keys table prints
 * per-key spend and caps in dollars, the model catalog prints rates in
 * dollars). The dashboard used to print the raw integer instead, so the same
 * balance read as "99,996,364,207" on one page and a fraction of a dollar on
 * the next, with nothing on either page relating the two.
 *
 * The credit figure is kept, below the money, with the conversion stated
 * beside it. Credits are the unit the ledger, the invoices and the per-key
 * caps are denominated in, so dropping them would make those surfaces
 * unreadable; printing them without the rate is what made this one unreadable.
 */
export function CreditBalance({ balance }: { balance: BalanceSummary }) {
  return (
    <div className="flex flex-col gap-1">
      <p className="metric text-3xl text-[var(--color-ink)]" data-numeric>
        {formatUsdBalanceFromCredits(balance.available_credits)}
      </p>
      <p className="text-xs text-[var(--color-ink-3)]">
        Posted{" "}
        <span className="metric text-[var(--color-ink-2)]">
          {formatUsdBalanceFromCredits(balance.posted_credits)}
        </span>{" "}
        · Reserved{" "}
        <span className="metric text-[var(--color-ink-2)]">
          {formatUsdBalanceFromCredits(balance.reserved_credits)}
        </span>
      </p>
      <p className="text-xs text-[var(--color-ink-3)]">
        {formatCredits(balance.available_credits)} credits, at{" "}
        {formatCredits(CREDITS_PER_USD)} credits per $1.00
      </p>
    </div>
  );
}
