/**
 * Issue #1332: the dashboard rendered the balance as a bare grouped integer
 * ("99,996,364,207") with the unit only in the card title, while the API keys
 * table rendered the same quantity in dollars ("$0.000662"). One denomination
 * now, dollars, with the credit figure and the conversion under it so the two
 * surfaces can be reconciled by eye.
 */
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { CreditBalance } from "@/components/billing/credit-balance";
import { SUB_CENT_BALANCE } from "@/lib/format/credits";

// The workspace balance observed live on the demo box, 2026-08-29.
const balance = {
  available_credits: 99_996_364_207,
  posted_credits: 100_000_000_000,
  reserved_credits: 3_635_793,
};

describe("CreditBalance", () => {
  it("leads with dollars, the denomination the rest of the console uses", () => {
    render(<CreditBalance balance={balance} />);
    expect(screen.getByText("$99.99")).toBeTruthy();
  });

  it("does not print a bare credit integer as the headline figure", () => {
    const { container } = render(<CreditBalance balance={balance} />);
    const metric = container.querySelector("p[data-numeric]");
    expect(metric?.textContent?.trim()).toBe("$99.99");
  });

  it("keeps the credit figure, with the conversion beside it", () => {
    render(<CreditBalance balance={balance} />);
    expect(
      screen.getByText(/99,996,364,207 credits, at 1,000,000,000 credits per \$1.00/),
    ).toBeTruthy();
  });

  it("draws posted and reserved in the same denomination as the headline", () => {
    render(<CreditBalance balance={balance} />);
    expect(screen.getByText("$100.00")).toBeTruthy();
    // The reserved figure is 3,635,793 credits, a third of a cent. It used to
    // render "$0.00363", one of the nine-significant-figure amounts the
    // formatter produced below a cent. Money reads in cents, so a sub-cent
    // amount now reads as a bound, which still distinguishes it from the
    // "$0.00" that nothing reserved would render.
    expect(screen.getByText(SUB_CENT_BALANCE)).toBeTruthy();
  });
});
