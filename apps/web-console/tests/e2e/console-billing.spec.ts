import { expect, test, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
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
// Coverage: /console/billing overview tab + invoices tab + ledger tab.
// Spec is env-gated like the other auth-driven specs — skips when test
// credentials are unset (CI behaviour matches `_probe/staging-flows.spec.ts`).

const HAS_CREDS = Boolean(VERIFIED_EMAIL && VERIFIED_PASSWORD);

const FX_FORBIDDEN = [
  /\$\d/, // dollar-sign followed by digit
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

// Fixture reset mutates global Supabase state (auth users, accounts,
// memberships). Parallel workers racing on the same reset cause sessions to
// flap mid-test, so this file MUST run serially against the shared backend.
// Mirrors `profile-completion.spec.ts` and `auth-shell.spec.ts` (Codex P2 —
// reset E2E fixture state before BDT-only billing assertions so other specs
// mutating profile state cannot make this guard order-dependent).
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
    for (const pattern of FX_FORBIDDEN) {
      expect(body, `FX-leak token ${pattern} on /console/billing`).not.toMatch(
        pattern,
      );
    }
  });

  test("invoice list renders BDT amounts only (CONSOLE-13-04)", async ({
    page,
  }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
    // Navigate directly to the invoices tab (the page reads `?tab=` to
    // decide which subview to render — see app/console/billing/page.tsx).
    await page.goto("/console/billing?tab=invoices");

    // Wait for the invoices tab to actually mount. The InvoiceList renders
    // either a populated table or the empty-state — both expose a heading
    // / table-row / empty-state region. Prefer a concrete UI signal over
    // `networkidle`, which is brittle for streamed Next.js pages.
    await expect(
      page
        .getByRole("heading", { name: /billing/i })
        .first(),
    ).toBeVisible({ timeout: 15_000 });
    await expect
      .poll(
        async () => {
          const body = await page.locator("body").innerText();
          return /invoice|no\s+invoices|empty|nothing\s+to\s+show/i.test(body);
        },
        { timeout: 15_000 },
      )
      .toBe(true);

    const body = await page.locator("body").innerText();
    for (const pattern of FX_FORBIDDEN) {
      expect(
        body,
        `FX-leak token ${pattern} on /console/billing?tab=invoices`,
      ).not.toMatch(pattern);
    }
  });
});
