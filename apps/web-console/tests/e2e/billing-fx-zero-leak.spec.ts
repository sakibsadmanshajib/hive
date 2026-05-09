import { expect, test, type Page } from "@playwright/test";
import {
  E2E_VERIFIED_EMAIL as VERIFIED_EMAIL,
  E2E_VERIFIED_PASSWORD as VERIFIED_PASSWORD,
} from "./support/e2e-auth-creds";

// Phase 17 FX-17-08 — billing surface FX/USD zero-leak Playwright spec.
//
// Companion to the in-process Go integration test
// (apps/control-plane/internal/payments/integration_fx_zero_leak_test.go) —
// here we drive the *full proxied path* a browser-side BD customer hits
// when loading /console/billing, then fetch the underlying control-plane
// JSON via the proxy and assert the response bytes carry NONE of the
// FX-tripwire keys.
//
// Distinct from console-fx-guard.spec.ts (which sweeps DOM across all
// console routes) by:
//   1. Fetching the raw API JSON, not just rendered DOM, so leaks below
//      the rendering layer are caught.
//   2. Targeting the billing surface specifically — checkout rails +
//      invoice list — which is where USD pricing was historically
//      surfaced.
//
// Spec is env-gated. Skips when E2E_VERIFIED_EMAIL/PASSWORD absent
// (CI without seeded fixtures, local without a logged-in account).

const HAS_CREDS = Boolean(VERIFIED_EMAIL && VERIFIED_PASSWORD);

const FX_FORBIDDEN: ReadonlyArray<RegExp> = [
  /amount_usd/i,
  /price_per_credit_usd/i,
  /exchange_rate/i,
  /\bfx_/i,
];

async function signIn(
  page: Page,
  email: string,
  password: string,
): Promise<void> {
  await page.goto("/auth/sign-in");
  await page.locator("#email").fill(email);
  await page.locator("#password").fill(password);
  await page.click('button[type="submit"]');
  await page.waitForURL((url) => url.pathname.startsWith("/console"), {
    timeout: 25_000,
  });
}

test.describe("billing FX/USD zero-leak (FX-17-08)", () => {
  test.skip(!HAS_CREDS, "E2E_VERIFIED_EMAIL/PASSWORD not set");

  test("checkout rails JSON carries no FX-tripwire keys", async ({ page }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);

    // Navigate to billing so the session+CSRF state is hydrated, then
    // fetch the rails JSON via the proxy that the browser uses.
    await page.goto("/console/billing", { waitUntil: "domcontentloaded" });
    await expect(page.locator("h1, h2").first()).toBeVisible({
      timeout: 15_000,
    });

    const responseBody: string = await page.evaluate(async () => {
      const response = await fetch(
        "/api/v1/accounts/current/checkout/rails",
        { credentials: "include" },
      );
      return await response.text();
    });

    expect(
      responseBody.length,
      "checkout rails response empty — auth session likely not propagated",
    ).toBeGreaterThan(0);

    for (const pattern of FX_FORBIDDEN) {
      expect(
        responseBody.match(pattern),
        `checkout rails JSON leaks ${pattern} to BD account customer surface`,
      ).toBeNull();
    }
  });

  test("billing page DOM carries no FX-tripwire keys for BD account", async ({
    page,
  }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);

    await page.goto("/console/billing", { waitUntil: "domcontentloaded" });
    await expect(page.locator("h1, h2").first()).toBeVisible({
      timeout: 15_000,
    });

    const html = await page.content();
    for (const pattern of FX_FORBIDDEN) {
      expect(
        html.match(pattern),
        `billing DOM leaks ${pattern} to BD account customer surface`,
      ).toBeNull();
    }
  });
});
