import { describe, expect, it } from "vitest";

import type { Invoice } from "@/lib/control-plane/types";

// Phase 13 FIX-13-01 regression guard.
//
// The customer-surface `Invoice` interface intentionally omits any FX/USD
// field. BD accounts must never see USD or any FX conversion language
// (regulatory rule, CONSOLE-13-04). This test enforces the guarantee at
// three layers — type-level (build fails), structural (object-literal
// excess-property), and runtime (key inspection) — so neither a required
// nor an *optional* FX field can be reintroduced silently.

// --- Type-level guard ---------------------------------------------------
//
// Catches reintroduction of *any* FX field on `Invoice`, including
// optional ones (e.g. `amount_usd?: number`). Object-literal checks alone
// miss optional fields because omitting them still type-checks; the only
// way to reject them is to assert at the keyof level.
type FxForbiddenKey =
  | "amount_usd"
  | "amount_USD"
  | "fx_rate"
  | "exchange_rate"
  | `fx_${string}`
  | `${string}_usd`;

type ForbiddenKeysOnInvoice = Extract<keyof Invoice, FxForbiddenKey>;

// `[T] extends [never]` evaluates `true` only when the union T is empty.
// If the build ever resolves this to `false`, an FX field has crept back
// onto `Invoice` and this assignment fails at typecheck time.
type AssertNoFxKeys = [ForbiddenKeysOnInvoice] extends [never] ? true : false;
const _assertNoFxKeys: AssertNoFxKeys = true;
void _assertNoFxKeys;

describe("Invoice surface — FX-leak regression guard (CONSOLE-13-04)", () => {
  it("does not expose any USD / FX field on the customer surface", () => {
    // Build a structurally valid Invoice. Excess-property checks reject
    // any unknown key here — keeps a required FX field from sneaking in.
    const invoice: Invoice = {
      id: "inv_01J...",
      invoice_number: "HIVE-INV-0001",
      status: "paid",
      credits: 1000,
      amount_local: 1100,
      local_currency: "BDT",
      tax_treatment: "vat_exclusive",
      rail: "bkash",
      line_items: [],
      created_at: "2026-04-25T00:00:00Z",
    };

    // Runtime guard: reading `amount_usd` from a plain Invoice returns
    // undefined — no field is present, no FX value reaches the customer
    // surface. Casting to a permissive shape preserves strict-TS hygiene
    // while letting the test reach for the absent key.
    const probe: Record<string, unknown> = {
      ...invoice,
    };
    expect(probe.amount_usd).toBeUndefined();
    expect(probe.fx_rate).toBeUndefined();
    expect(probe.exchange_rate).toBeUndefined();

    // Property-list guard — every Invoice key is an explicit BDT-safe field.
    const keys = Object.keys(invoice).sort();
    expect(keys).toEqual(
      [
        "amount_local",
        "created_at",
        "credits",
        "id",
        "invoice_number",
        "line_items",
        "local_currency",
        "rail",
        "status",
        "tax_treatment",
      ].sort(),
    );
  });
});
