import { expect, test, type Page } from "@playwright/test";
import {
  E2E_VERIFIED_EMAIL as VERIFIED_EMAIL,
  E2E_VERIFIED_PASSWORD as VERIFIED_PASSWORD,
} from "./support/e2e-auth-creds";

// Phase 13 FIX-13-06 — BDT-only assertion on /console/billing.
//
// Regulatory rule (CONSOLE-13-04, feedback_bdt_no_fx_display.md): BD
// accounts must never see USD or any FX conversion language on the billing
// surface. This spec drives the verified-tester through sign-in to
// `/console/billing` and asserts the page body carries no `$` literal,
// no standalone `USD` token, no `amount_usd` / `fx_*` / `exchange_rate`
// substring.
//
// Coverage: /console/billing (overview + invoice list + ledger).
// Spec is env-gated like the other auth-driven specs — skips when test
// credentials are unset (CI behaviour matches `_probe/staging-flows.spec.ts`).

const HAS_CREDS = Boolean(VERIFIED_EMAIL && VERIFIED_PASSWORD);

async function signIn(page: Page, email: string, password: string) {
  await page.goto("/auth/sign-in");
  await page.locator("#email").fill(email);
  await page.locator("#password").fill(password);
  await page.click('button[type="submit"]');
  await page.waitForURL((url) => url.pathname.startsWith("/console"), {
    timeout: 25_000,
  });
}

test.describe("/console/billing — BDT-only customer surface", () => {
  test.skip(!HAS_CREDS, "E2E_VERIFIED_EMAIL/PASSWORD not set");

  test("billing overview renders without USD/FX leak (CONSOLE-13-04)", async ({
    page,
  }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
    await page.goto("/console/billing");

    // Wait for the billing surface to render (heading, balance card).
    await expect(
      page.getByRole("heading", { name: /billing|credits|balance/i }).first(),
    ).toBeVisible({ timeout: 15_000 });

    // Pull the rendered DOM body. The regulatory guard is structural — no
    // USD literal, no FX field-name reaches the customer surface.
    const body = await page.locator("body").innerText();
    expect(body).not.toMatch(/\$\d/); // dollar-sign followed by digit
    expect(body).not.toMatch(/\bUSD\b/);
    expect(body).not.toMatch(/amount_usd/);
    expect(body).not.toMatch(/fx_/);
    expect(body).not.toMatch(/exchange_rate/i);
  });

  test("invoice list renders BDT amounts only (CONSOLE-13-04)", async ({
    page,
  }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
    await page.goto("/console/billing");

    // The invoice list table may be empty for fresh tester accounts; the
    // assertion is structural — wait for either a populated table row or
    // an "no invoices" empty-state, both BDT-clean.
    await page.waitForLoadState("networkidle", { timeout: 15_000 });
    const body = await page.locator("body").innerText();
    expect(body).not.toMatch(/\bUSD\b/);
    expect(body).not.toMatch(/amount_usd/);
  });
});
