import { describe, it, expect } from "vitest";
import { SpendAlertForm } from "./spend-alert-form";

describe("SpendAlertForm BDT compliance", () => {
  it("module exports the SpendAlertForm component", () => {
    expect(typeof SpendAlertForm).toBe("function");
  });

  // Threshold values are typed as a const tuple in the form module. The
  // regulatory + product invariant: the UI offers exactly 50/80/100 — the
  // backend enforces the same set in CreateAlert.ErrInvalidThreshold.
  it("source asserts 50/80/100 threshold set", async () => {
    const fs = await import("node:fs");
    const path = await import("node:path");
    const src = fs.readFileSync(
      path.join(__dirname, "spend-alert-form.tsx"),
      "utf8",
    );
    expect(src).toContain("value: 50");
    expect(src).toContain("value: 80");
    expect(src).toContain("value: 100");
  });

  it("non-comment source carries no USD/FX strings", async () => {
    const fs = await import("node:fs");
    const path = await import("node:path");
    const raw = fs.readFileSync(
      path.join(__dirname, "spend-alert-form.tsx"),
      "utf8",
    );
    // Strip line + block comments before scanning. The audit target is the
    // RUNTIME surface — comments documenting the rule itself are allowed.
    const src = raw
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/(^|\n)\s*\/\/[^\n]*/g, "$1");
    expect(src).not.toContain("amount_usd");
    expect(src).not.toMatch(/\bUSD\b/);
    expect(src).not.toMatch(/\$\d/);
    expect(src).not.toMatch(/\bfx_/);
    expect(src).not.toContain("exchange_rate");
  });
});
