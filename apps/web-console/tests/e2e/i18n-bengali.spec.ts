/**
 * i18n-bengali.spec.ts
 *
 * Guards the Bengali locale wiring against silent English fallback. The
 * failure mode this catches is a request-config or provider regression where
 * the locale cookie is still set but every string renders in English, which
 * looks like nothing is wrong until a BD customer opens the console.
 *
 * Mirrors the fixture/sign-in pattern from auth-shell.spec.ts.
 */

import { test, expect } from "@playwright/test";
import { reseedFixtures } from "./support/fixture-reset";
import {
  E2E_VERIFIED_EMAIL as VERIFIED_EMAIL,
  E2E_VERIFIED_PASSWORD as VERIFIED_PASSWORD,
} from "./support/e2e-auth-creds";

// Must match messages/bn.json -> Nav.overview.
const BENGALI_OVERVIEW = "ওভারভিউ";

async function useBengali(page: import("@playwright/test").Page) {
  const baseURL = test.info().project.use.baseURL ?? "http://localhost:3000";
  await page.context().addCookies([
    { name: "locale", value: "bn", url: baseURL },
  ]);
}

test("locale cookie drives the document language without signing in", async ({
  page,
}) => {
  await useBengali(page);
  await page.goto("/auth/sign-in");

  await expect(page.locator("html")).toHaveAttribute("lang", "bn");
});

test.describe("Bengali console chrome", () => {
  test.skip(
    !VERIFIED_EMAIL || !VERIFIED_PASSWORD,
    "E2E_VERIFIED_EMAIL/PASSWORD not set"
  );

  // Fixture reset mutates global Supabase state — run serially.
  test.describe.configure({ mode: "serial" });

  test.beforeEach(async ({}, testInfo) => {
    await reseedFixtures(testInfo);
  });

  test("nav renders Bengali labels and the switcher returns to English", async ({
    page,
  }) => {
    // Sign-in plus three console renders; the default 30s budget is not enough
    // when the suite runs against a dev server.
    test.setTimeout(150_000);

    await page.goto("/auth/sign-in");
    await page.locator("#email").fill(VERIFIED_EMAIL);
    await page.locator("#password").fill(VERIFIED_PASSWORD);
    await page.click('button[type="submit"]');
    await page.waitForURL((url) => url.pathname.startsWith("/console"), {
      timeout: 45000,
    });

    await useBengali(page);
    await page.goto("/console");

    const nav = page.locator("aside nav");
    await expect(page.locator("html")).toHaveAttribute("lang", "bn");
    await expect(nav).toContainText(BENGALI_OVERVIEW, { timeout: 30000 });

    // The switcher is the only way back, so a broken switch would trap a
    // viewer in a language they may not read.
    await page.getByRole("button", { name: /EN/ }).click();
    await expect(page.locator("html")).toHaveAttribute("lang", "en", {
      timeout: 30000,
    });
    await expect(nav).toContainText("Overview", { timeout: 30000 });
  });
});
