import { expect, test, type Page } from "@playwright/test";
import {
  E2E_VERIFIED_EMAIL as VERIFIED_EMAIL,
  E2E_VERIFIED_PASSWORD as VERIFIED_PASSWORD,
} from "./support/e2e-auth-creds";

// Phase 13 FIX-13-07 — whole-console FX-leak regression guard.
//
// Walks every console route in turn for a verified BD account and asserts
// the rendered DOM never carries `amount_usd`, `fx_*`, or `exchange_rate`
// tokens. The PHASE-17-OWNER-ONLY annotation marks internal pricing
// primitives as known leaks — this spec catches anything that escapes to
// the customer surface.
//
// Routes covered (from .planning/phases/13-console-integration-fixes/13-AUDIT.md
// Section A):
//   /console
//   /console/billing
//   /console/settings/billing
//   /console/settings/profile
//   /console/api-keys
//   /console/catalog
//   /console/analytics
//   /console/members
//   /console/setup
//
// Spec is env-gated — skips when verified credentials are absent.

const HAS_CREDS = Boolean(VERIFIED_EMAIL && VERIFIED_PASSWORD);

const ROUTES = [
  "/console",
  "/console/billing",
  "/console/settings/billing",
  "/console/settings/profile",
  "/console/api-keys",
  "/console/catalog",
  "/console/analytics",
  "/console/members",
  "/console/setup",
];

// All patterns case-insensitive — the guard's job is to catch *any* FX
// field-name spelling that escapes to the customer surface, regardless of
// upstream casing.
const FX_FORBIDDEN = [/amount_usd/i, /\bfx_/i, /exchange_rate/i];

async function signIn(page: Page, email: string, password: string) {
  await page.goto("/auth/sign-in");
  await page.locator("#email").fill(email);
  await page.locator("#password").fill(password);
  await page.click('button[type="submit"]');
  await page.waitForURL((url) => url.pathname.startsWith("/console"), {
    timeout: 25_000,
  });
}

test.describe("FX-leak whole-console guard (CONSOLE-13-04)", () => {
  test.skip(!HAS_CREDS, "E2E_VERIFIED_EMAIL/PASSWORD not set");

  test("no FX field-name leaks to any console route", async ({ page }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);

    for (const route of ROUTES) {
      await page.goto(route, { waitUntil: "domcontentloaded" });
      // Wait for a concrete UI signal instead of `networkidle`. Streamed
      // Next.js pages, polling fetches, and Supabase realtime can keep
      // network activity above the idle threshold past the timeout, causing
      // flakes. Every console route renders an <h1>/<h2> as soon as its
      // server-component shell hydrates — assert that instead.
      await expect(
        page.locator("h1, h2").first(),
      ).toBeVisible({ timeout: 15_000 });

      const html = await page.content();
      for (const pattern of FX_FORBIDDEN) {
        expect(
          html.match(pattern),
          `FX-leak token ${pattern} reached ${route} customer surface`,
        ).toBeNull();
      }
    }
  });
});
