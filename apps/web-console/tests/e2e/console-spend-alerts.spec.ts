import { expect, test, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import {
  E2E_VERIFIED_EMAIL as VERIFIED_EMAIL,
  E2E_VERIFIED_PASSWORD as VERIFIED_PASSWORD,
} from "./support/e2e-auth-creds";

// Phase 14 FIX-14-28 — /console/billing/alerts E2E.
//
// Asserts the spend-alert surface:
//   - heading + active-alerts table + create form render
//   - threshold dropdown lists 50/80/100
//   - email + webhook fields present
//   - FX-leak regex zero matches across the rendered DOM

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

test.describe("/console/billing/alerts — spend alert thresholds (Phase 14)", () => {
  test.skip(!HAS_CREDS, "E2E_VERIFIED_EMAIL/PASSWORD not set");

  test("alerts page renders with 50/80/100 threshold options", async ({
    page,
  }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
    await page.goto("/console/billing/alerts");

    await expect(
      page.getByRole("heading", { name: /spend alerts/i }).first(),
    ).toBeVisible({ timeout: 15_000 });

    const thresholdSelect = page.locator("#alert-threshold");
    await expect(thresholdSelect).toBeVisible();

    // The threshold dropdown ships exactly three options — Phase 14 contract.
    const optionValues = await thresholdSelect
      .locator("option")
      .evaluateAll((nodes) =>
        nodes.map((n) => (n as HTMLOptionElement).value),
      );
    expect(optionValues).toEqual(["50", "80", "100"]);

    await expect(page.locator("#alert-email")).toBeVisible();
    await expect(page.locator("#alert-webhook")).toBeVisible();
  });

  test("alerts page is BDT-only — no USD/FX leak", async ({ page }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
    await page.goto("/console/billing/alerts");
    await expect(
      page.getByRole("heading", { name: /spend alerts/i }).first(),
    ).toBeVisible({ timeout: 15_000 });

    const body = await page.locator("body").innerText();
    for (const pattern of FX_FORBIDDEN) {
      expect(
        body,
        `FX-leak token ${pattern} on /console/billing/alerts`,
      ).not.toMatch(pattern);
    }
  });
});
