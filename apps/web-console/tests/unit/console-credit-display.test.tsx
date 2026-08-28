import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";

import enMessages from "@/messages/en.json";

/*
 * The account balance is money, so it is drawn as money.
 *
 * Both balance cards used to render `available_credits` through
 * `formatCredits`, which is a thousand-separated integer and nothing else. At
 * D-046's unit (one US dollar is 1,000,000,000 credits) a demo account holding
 * 46 cents reads as "458,419,464", a nine digit number with no currency mark
 * and no unit word on the dashboard card at all. Every other price surface in
 * this console already renders credits as dollars through
 * `formatUsdFromCredits`, and so does the chat composer's credits banner
 * (vendor/open-webui/src/lib/hive/credits.ts), so the console overview was the
 * one place left showing the raw integer.
 *
 * These tests pin three things, and the third is the one that keeps the fix
 * honest:
 *
 *   1. The headline figure is the dollar amount.
 *   2. The exact credit count survives, labelled, because the ledger table
 *      directly below it is denominated in credits and a balance with no
 *      credit figure cannot be reconciled against it.
 *   3. A real balance never renders as "$0.00" or "$0". `formatUsdFromCredits`
 *      already carries that invariant for catalog prices; asserting it here
 *      states that a balance too small to show at two decimals is a number to
 *      widen, never a zero to print, because a customer reading zero stops
 *      sending traffic.
 */

vi.mock("@/components/locale-switcher", () => ({
  LocaleSwitcher: () => null,
}));

const viewerMock = vi.fn();
const profileMock = vi.fn();
const balanceMock = vi.fn();
const usageMock = vi.fn();
const errorsMock = vi.fn();

vi.mock("@/lib/control-plane/client", () => ({
  getViewer: () => viewerMock(),
  getAccountProfile: () => profileMock(),
  getBalance: () => balanceMock(),
  getAnalyticsUsage: () => usageMock(),
  getAnalyticsErrors: () => errorsMock(),
}));

import ConsolePage from "@/app/console/page";
import { BillingOverview } from "@/components/billing/billing-overview";
import type { AccountProfile, Viewer } from "@/lib/control-plane/client";

/** The figure observed on the live demo box on 2026-08-28: $0.458419464. */
const OBSERVED_CREDITS = 458_419_464;
const OBSERVED_POSTED = 460_000_000;
const OBSERVED_RESERVED = 1_580_536;

const viewer: Viewer = {
  user: { id: "u1", email: "demo@hive.invalid", email_verified: true },
  current_account: {
    id: "acct_1",
    display_name: "Hive Demo",
    slug: "hive-demo",
    account_type: "team",
    role: "owner",
  },
  memberships: [],
  permissions: [],
};

const profile: AccountProfile = {
  owner_name: "Demo Owner",
  login_email: "demo@hive.invalid",
  display_name: "Hive Demo",
  account_type: "team",
  country_code: "BD",
  state_region: "",
  profile_setup_complete: true,
};

async function renderOverview(balance: {
  available_credits: number;
  posted_credits: number;
  reserved_credits: number;
}) {
  viewerMock.mockResolvedValue(viewer);
  profileMock.mockResolvedValue(profile);
  balanceMock.mockResolvedValue(balance);
  usageMock.mockResolvedValue([]);
  errorsMock.mockResolvedValue([]);
  const element = await ConsolePage();
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      {element}
    </NextIntlClientProvider>,
  );
}

/** The card a heading belongs to, so an assertion cannot match another card. */
function cardFor(title: string): HTMLElement {
  const heading = screen.getByText(title);
  const card = heading.closest("div")?.parentElement;
  if (!card) {
    throw new Error(`no card around ${title}`);
  }
  return card as HTMLElement;
}

describe("Console overview: available credits", () => {
  it("leads with the dollar amount, not the raw credit integer", async () => {
    await renderOverview({
      available_credits: OBSERVED_CREDITS,
      posted_credits: OBSERVED_POSTED,
      reserved_credits: OBSERVED_RESERVED,
    });

    const card = cardFor("Available credits");
    const headline = card.querySelector("[data-numeric]");
    expect(headline).toBeTruthy();
    // Three significant digits, per formatUsdFromCredits. The point of the
    // assertion is the leading "$", which the integer render never had.
    expect(headline?.textContent).toBe("$0.458");
  });

  it("keeps the exact credit count, labelled, so the ledger can be reconciled", async () => {
    await renderOverview({
      available_credits: OBSERVED_CREDITS,
      posted_credits: OBSERVED_POSTED,
      reserved_credits: OBSERVED_RESERVED,
    });

    const card = cardFor("Available credits");
    expect(within(card).getByText("458,419,464")).toBeTruthy();
    expect(card.textContent).toContain("credits");
  });

  it("renders posted and reserved in dollars too, so one card carries one unit", async () => {
    await renderOverview({
      available_credits: OBSERVED_CREDITS,
      posted_credits: OBSERVED_POSTED,
      reserved_credits: OBSERVED_RESERVED,
    });

    const card = cardFor("Available credits");
    expect(within(card).getByText("$0.46")).toBeTruthy();
    expect(within(card).getByText("$0.00158")).toBeTruthy();
  });

  it("never prints a real balance as zero, however small it is", async () => {
    // One credit is a billionth of a dollar. Two decimals would render this
    // as "$0.00", which reads as an empty account rather than a tiny one.
    await renderOverview({
      available_credits: 1,
      posted_credits: 1,
      reserved_credits: 0,
    });

    const card = cardFor("Available credits");
    const headline = card.querySelector("[data-numeric]");
    expect(headline?.textContent).toBe("$0.000000001");
  });

  it("prints an exactly empty balance as $0, which is a fact rather than a rounding", async () => {
    await renderOverview({
      available_credits: 0,
      posted_credits: 0,
      reserved_credits: 0,
    });

    const card = cardFor("Available credits");
    const headline = card.querySelector("[data-numeric]");
    expect(headline?.textContent).toBe("$0");
    expect(within(card).getByText("0")).toBeTruthy();
  });
});

describe("Billing page: available balance", () => {
  const renderBilling = (balance: {
    available_credits: number;
    posted_credits: number;
    reserved_credits: number;
  }) =>
    render(
      <BillingOverview
        balance={balance}
        recentEntries={[]}
        accountCountryCode="BD"
      />,
    );

  it("leads with the dollar amount on the billing page too", () => {
    // Same card, second surface. Fixing only the dashboard would leave the
    // page a customer actually opens to check their money unchanged.
    const { container } = renderBilling({
      available_credits: OBSERVED_CREDITS,
      posted_credits: OBSERVED_POSTED,
      reserved_credits: OBSERVED_RESERVED,
    });

    const headline = container.querySelector("[data-numeric]");
    expect(headline?.textContent).toBe("$0.458");
  });

  it("keeps the exact credit count beside it, labelled", () => {
    renderBilling({
      available_credits: OBSERVED_CREDITS,
      posted_credits: OBSERVED_POSTED,
      reserved_credits: OBSERVED_RESERVED,
    });

    expect(screen.getByText("458,419,464")).toBeTruthy();
    expect(screen.getAllByText(/credits/i).length).toBeGreaterThan(0);
  });

  it("renders posted and reserved in dollars", () => {
    renderBilling({
      available_credits: OBSERVED_CREDITS,
      posted_credits: OBSERVED_POSTED,
      reserved_credits: OBSERVED_RESERVED,
    });

    expect(screen.getByText("$0.46")).toBeTruthy();
    expect(screen.getByText("$0.00158")).toBeTruthy();
  });
});
