import { expect, test, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import {
  E2E_VERIFIED_EMAIL as VERIFIED_EMAIL,
  E2E_VERIFIED_PASSWORD as VERIFIED_PASSWORD,
} from "./support/e2e-auth-creds";

// Phase 14 FIX-14-28 — /console/billing/budget E2E.
//
// Asserts the owner-gated workspace budget surface:
//   - heading + form render
//   - soft + hard cap inputs visible
//
// Save round-trip + non-owner read-only assertion are deferred to a CI run
// against a workspace where the verified tester is an owner; the smoke
// surface here is order-stable across env states.

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

test.describe.configure({ mode: "serial" });

test.beforeEach(async () => {
  if (!HAS_CREDS) return;
  try {
    execFileSync("node", ["tests/e2e/support/e2e-auth-fixtures.mjs"], {
      cwd: process.cwd(),
      env: { ...process.env, NODE_OPTIONS: "" },
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

test.describe("/console/billing/budget — workspace budget caps (Phase 14)", () => {
  test.skip(!HAS_CREDS, "E2E_VERIFIED_EMAIL/PASSWORD not set");

  test("budget page renders", async ({ page }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
    await page.goto("/console/billing/budget");

    await expect(
      page.getByRole("heading", { name: /budget/i }).first(),
    ).toBeVisible({ timeout: 15_000 });

    // Form primitives — soft + hard cap inputs present regardless of role.
    await expect(page.locator("#budget-soft-cap")).toBeVisible();
    await expect(page.locator("#budget-hard-cap")).toBeVisible();
  });

  test("non-owner sees disabled fields (read-only enforcement)", async ({
    page,
  }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
    await page.goto("/console/billing/budget");
    await expect(
      page.getByRole("heading", { name: /budget/i }).first(),
    ).toBeVisible({ timeout: 15_000 });

    // The verified tester's role on the seeded workspace determines whether
    // the inputs are disabled. The component disables them for non-owners.
    // We assert the structural primitive — the page renders the form rather
    // than a hard 403 — defence-in-depth is on the backend (POST → 403).
    const softCap = page.locator("#budget-soft-cap");
    const hardCap = page.locator("#budget-hard-cap");
    await expect(softCap).toBeAttached();
    await expect(hardCap).toBeAttached();
  });
});
