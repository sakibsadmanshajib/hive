import { expect, test, type Page } from "@playwright/test";
import {
  E2E_VERIFIED_EMAIL as VERIFIED_EMAIL,
  E2E_VERIFIED_PASSWORD as VERIFIED_PASSWORD,
} from "./support/e2e-auth-creds";

// Phase 17 FX-17-08 — billing surface checkout-rails contract Playwright
// spec.
//
// Drives the *full proxied path* a browser-side BD customer hits when
// loading /console/billing, then fetches the underlying control-plane JSON
// via the proxy and asserts the CheckoutOptions wire shape the console
// depends on (currency, price_per_block_minor, credit_block_size).
//
// Spec is env-gated. Skips when E2E_VERIFIED_EMAIL/PASSWORD absent
// (CI without seeded fixtures, local without a logged-in account).

const HAS_CREDS = Boolean(VERIFIED_EMAIL && VERIFIED_PASSWORD);

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

test.describe("billing checkout-rails contract (FX-17-08)", () => {
  test.skip(!HAS_CREDS, "E2E_VERIFIED_EMAIL/PASSWORD not set");

  test("checkout rails JSON carries the required CheckoutOptions keys", async ({
    page,
  }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);

    // Navigate to billing so the session+CSRF state is hydrated, then
    // fetch the rails JSON via the proxy that the browser uses.
    await page.goto("/console/billing", { waitUntil: "domcontentloaded" });
    await expect(page.locator("h1, h2").first()).toBeVisible({
      timeout: 15_000,
    });

    // Assert (a) the fetch actually succeeded — 200 + application/json —
    // so we are not silently passing on a redirected sign-in HTML page,
    // and (b) the positive contract shape the console depends on.
    const rails = await page.evaluate(async () => {
      const response = await fetch(
        "/api/v1/accounts/current/checkout/rails",
        { credentials: "include" },
      );
      return {
        ok: response.ok,
        status: response.status,
        contentType: response.headers.get("content-type") ?? "",
        body: await response.text(),
      };
    });

    expect(rails.ok, "checkout rails fetch failed").toBeTruthy();
    expect(rails.status, "checkout rails status must be 200").toBe(200);
    expect(rails.contentType.toLowerCase()).toContain("application/json");

    const responseBody = rails.body;
    expect(
      responseBody.length,
      "checkout rails response empty — auth session likely not propagated",
    ).toBeGreaterThan(0);

    expect(responseBody).toContain('"currency"');
    expect(responseBody).toContain('"price_per_block_minor"');
    expect(responseBody).toContain('"credit_block_size"');
  });
});
