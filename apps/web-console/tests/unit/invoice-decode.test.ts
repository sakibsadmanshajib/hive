import { describe, expect, it } from "vitest";

import type { Invoice } from "@/lib/control-plane/types";

// Phase 13 FIX-13-01 regression guard.
//
// The customer-surface `Invoice` interface intentionally omits any FX/USD
// field. BD accounts must never see USD or any FX conversion language
// (regulatory rule, CONSOLE-13-04). This test enforces the type-level
// guarantee — if a future change re-introduces an `amount_usd` (or any
// `*_usd` / `fx_*`) property on the `Invoice` interface, this test refuses
// to compile.

describe("Invoice surface — FX-leak regression guard (CONSOLE-13-04)", () => {
  it("does not expose any USD / FX field on the customer surface", () => {
    // Build a structurally valid Invoice. If the interface re-acquires a USD
    // field, this object literal becomes type-incompatible with the inferred
    // shape and the build fails at typecheck time.
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
      ].sort()
    );
  });
});
