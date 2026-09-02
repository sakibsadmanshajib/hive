/**
 * Issue #1694: the balance card rendered US dollars and then stated the
 * conversion under it, in the words "1,000,000,000 credits per $1.00". That
 * line published the credit peg, and from the peg every internal figure
 * follows. The card renders Hive credits now, and no currency at all (owner
 * ruling, .wolf/decisions.md D-070).
 */
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { CreditBalance } from "@/components/billing/credit-balance";
import { CURRENCY_MARK } from "@/tests/support/currency-mark";

// The workspace balance observed live on the demo box, 2026-08-29.
const balance = {
  available_credits: 99_996_364_207,
  posted_credits: 100_000_000_000,
  reserved_credits: 3_635_793,
};

describe("CreditBalance", () => {
  it("leads with the credit balance, the unit the ledger moves", () => {
    const { container } = render(<CreditBalance balance={balance} />);
    const metric = container.querySelector("p[data-numeric]");
    expect(metric?.textContent?.trim()).toBe("99,996,364,207 credits");
  });

  it("draws posted and reserved in the same unit as the headline", () => {
    render(<CreditBalance balance={balance} />);
    expect(screen.getByText("100,000,000,000 credits")).toBeTruthy();
    expect(screen.getByText("3,635,793 credits")).toBeTruthy();
  });

  it("states no currency figure and no conversion rate anywhere", () => {
    // The guard, and the reason this file exists. Not "the headline is not a
    // dollar amount", which a second dollar-denominated line beside it would
    // still satisfy: nothing in this card may carry a currency mark, and the
    // peg sentence in particular is gone rather than reworded.
    const { container } = render(<CreditBalance balance={balance} />);
    const text = container.textContent ?? "";
    expect(text).not.toMatch(CURRENCY_MARK);
    expect(text).not.toMatch(/per \$1|credits per|1,000,000,000 credits per/);
  });
});
