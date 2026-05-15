/**
 * FX-17-08 — web-console FX/USD zero-leak unit test.
 *
 * Renders the customer-facing billing overview component (the closest
 * web-console route surface to a "usage page" — see app/console/billing/page.tsx
 * which is the canonical balance + ledger landing) under React Testing
 * Library, and asserts the rendered HTML carries NONE of the FX-tripwire
 * tokens.
 *
 * Pairs with the integration test in apps/control-plane/internal/payments/
 * (server side), the source-guard in components/billing/checkout-modal.test.tsx
 * (FX-17-04 wire-shape lock for `lib/control-plane/client.ts`), and the
 * Playwright spec in tests/e2e/ (browser side).
 *
 * Strict TS: NO `as`, NO `any`, NO `unknown` casts. Mock data is built
 * structurally against the exported `BalanceSummary` and `LedgerEntry`
 * types from lib/control-plane/client.ts.
 */

import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { BillingOverview } from "../../components/billing/billing-overview";
import type {
  BalanceSummary,
  LedgerEntry,
} from "../../lib/control-plane/client";

const FX_BANNED_KEYS = [
  "amount_usd",
  "usd_",
  "fx_",
  "price_per_credit_usd",
  "exchange_rate",
];

describe("BillingOverview (FX-17-08 customer surface)", () => {
  const balance: BalanceSummary = {
    posted_credits: 100_000,
    reserved_credits: 5_000,
    available_credits: 95_000,
  };

  const recentEntries: LedgerEntry[] = [
    {
      id: "11111111-2222-3333-4444-555555555555",
      entry_type: "grant",
      credits_delta: 100_000,
      idempotency_key: "test-idem-1",
      request_id: "req-1",
      metadata: { rail: "bkash" },
      created_at: "2026-05-01T00:00:00Z",
    },
    {
      id: "22222222-3333-4444-5555-666666666666",
      entry_type: "usage_charge",
      credits_delta: -2_500,
      idempotency_key: "test-idem-2",
      request_id: "req-2",
      metadata: { model_id: "gpt-4o-mini" },
      created_at: "2026-05-02T00:00:00Z",
    },
  ];

  it("renders BD account view with no FX-leaking JSON keys in DOM", () => {
    const { container } = render(
      <BillingOverview
        balance={balance}
        recentEntries={recentEntries}
        accountCountryCode="BD"
      />,
    );

    const html = container.innerHTML;
    for (const banned of FX_BANNED_KEYS) {
      expect(
        html.includes(banned),
        `BillingOverview leaked FX token "${banned}" to DOM`,
      ).toBe(false);
    }
  });

  it("renders non-BD account view with no FX-leaking JSON keys in DOM", () => {
    const { container } = render(
      <BillingOverview
        balance={balance}
        recentEntries={recentEntries}
        accountCountryCode="US"
      />,
    );

    const html = container.innerHTML;
    for (const banned of FX_BANNED_KEYS) {
      expect(
        html.includes(banned),
        `BillingOverview (US) leaked FX token "${banned}" to DOM`,
      ).toBe(false);
    }
  });

  it("renders no FX language strings (rate / exchange / conversion) in DOM", () => {
    const { container } = render(
      <BillingOverview
        balance={balance}
        recentEntries={recentEntries}
        accountCountryCode="BD"
      />,
    );

    const html = container.innerHTML.toLowerCase();
    for (const phrase of ["fx rate", "exchange rate", "conversion rate"]) {
      expect(html.includes(phrase)).toBe(false);
    }
  });
});
