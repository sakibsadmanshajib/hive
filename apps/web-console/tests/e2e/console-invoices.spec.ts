import { expect, test, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import {
  E2E_VERIFIED_EMAIL as VERIFIED_EMAIL,
  E2E_VERIFIED_PASSWORD as VERIFIED_PASSWORD,
} from "./support/e2e-auth-creds";

// Phase 14 FIX-14-28 — /console/billing/invoices E2E.
//
// Asserts the workspace invoices surface:
//   - heading + table or empty-state render
//   - body never matches USD / $ / fx_ / exchange_rate
//
// Download click is asserted structurally (anchor href targets the proxy
// route). The actual PDF byte-sniff happens in the integration test on the
// control-plane side (Phase 14 Task 4).

const HAS_CREDS = Boolean(VERIFIED_EMAIL && VERIFIED_PASSWORD);

const FX_FORBIDDEN = [
  /\$\d/,
  /\bUSD\b/i,
  /amount_usd/i,
  /\bfx_/i,
  /exchange_rate/i,
];

async function signIn(page: Page, email: string, password: string) {
  await page.goto("/auth/sign-in");
  await page.locator("#email").fill(email);
  await page.locator("#password").fill(password);
  await page.click('button[type="submit"]');
  await page.waitForURL((url) => url.pathname.startsWith("/console"), {
    timeout: 25_000,
  });
}

test.describe.configure({ mode: "serial" });

test.beforeEach(async () => {
  if (!HAS_CREDS) return;
  try {
    execFileSync("node", ["tests/e2e/support/e2e-auth-fixtures.mjs"], {
      cwd: process.cwd(),
      env: process.env,
      stdio: "pipe",
    });
  } catch (err: unknown) {
    const e = err as { stdout?: Buffer; stderr?: Buffer };
    process.stderr.write(
      `[e2e-auth-fixtures] reset failed\n${e.stdout ?? ""}${e.stderr ?? ""}\n`,
    );
    throw err;
  }
});

test.describe("/console/billing/invoices — workspace invoices (Phase 14)", () => {
  test.skip(!HAS_CREDS, "E2E_VERIFIED_EMAIL/PASSWORD not set");

  test("invoices page renders BDT-only", async ({ page }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
    await page.goto("/console/billing/invoices");

    await expect(
      page.getByRole("heading", { name: /invoices/i }).first(),
    ).toBeVisible({ timeout: 15_000 });

    // Either a populated table (rows with download links) OR the empty-state
    // copy. Both surfaces are BDT-only by construction.
    await expect
      .poll(
        async () => {
          const body = await page.locator("body").innerText();
          return /no invoices|workspace invoices|download/i.test(body);
        },
        { timeout: 15_000 },
      )
      .toBe(true);

    const body = await page.locator("body").innerText();
    for (const pattern of FX_FORBIDDEN) {
      expect(
        body,
        `FX-leak token ${pattern} on /console/billing/invoices`,
      ).not.toMatch(pattern);
    }
  });

  test("download links target the proxy route", async ({ page }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
    await page.goto("/console/billing/invoices");
    await expect(
      page.getByRole("heading", { name: /invoices/i }).first(),
    ).toBeVisible({ timeout: 15_000 });

    // If invoices are seeded, every Download link must point at the
    // /api/invoices/{id}/pdf proxy. If empty, this assertion is vacuously
    // satisfied (count=0) — both states are valid for the smoke spec.
    const links = page.getByRole("link", { name: /download pdf/i });
    const count = await links.count();
    for (let i = 0; i < count; i += 1) {
      const href = await links.nth(i).getAttribute("href");
      expect(href).toMatch(/^\/api\/invoices\/[^/]+\/pdf$/);
    }
  });
});
