import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import {
  E2E_VERIFIED_EMAIL as VERIFIED_EMAIL,
  E2E_VERIFIED_PASSWORD as VERIFIED_PASSWORD,
  E2E_UNVERIFIED_EMAIL as UNVERIFIED_EMAIL,
  E2E_UNVERIFIED_PASSWORD as UNVERIFIED_PASSWORD,
} from "./support/e2e-auth-creds";

async function signIn(
  page: import("@playwright/test").Page,
  email: string,
  password: string
) {
  await page.goto("/auth/sign-in");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.click('button[type="submit"]');
  await page.waitForURL("**/console**");
}

test.beforeEach(async () => {
  try {
    execFileSync("node", ["tests/e2e/support/e2e-auth-fixtures.mjs"], {
      cwd: process.cwd(),
      env: process.env,
      stdio: "pipe",
    });
  } catch (err: unknown) {
    const e = err as { stdout?: Buffer; stderr?: Buffer };
    process.stderr.write(
      `[e2e-auth-fixtures] reset failed\n${e.stdout ?? ""}${e.stderr ?? ""}\n`
    );
    throw err;
  }
});

test.describe("profile completion", () => {
  test.describe("setup saves profile", () => {
    test.skip(!VERIFIED_EMAIL || !VERIFIED_PASSWORD, "E2E verified credentials not set");

    test("setup saves profile", async ({ page }) => {
      await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
      await page.goto("/console/setup");

      await page.fill('input[name="ownerName"]', "Alice Smith");
      await page.fill('input[name="accountName"]', "Acme Labs");
      await page.selectOption('select[name="accountType"]', "business");
      await page.fill('input[name="countryCode"]', "US");
      await page.fill('input[name="stateRegion"]', "CA");
      await page.click('button[type="submit"]');

      await page.waitForURL("**/console");
      await expect(page.getByText("Workspace:")).toBeVisible();
      await expect(page.getByRole("link", { name: "Complete setup" })).toHaveCount(0);
    });
  });

  test.describe("dashboard shows setup reminder instead of forcing setup after completion", () => {
    test.skip(!VERIFIED_EMAIL || !VERIFIED_PASSWORD, "E2E verified credentials not set");

    test("dashboard shows setup reminder instead of forcing setup after completion", async ({
      page,
    }) => {
      await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
      await page.goto("/console");

      await expect(page.getByRole("link", { name: "Complete setup" })).toBeVisible();

      await page.goto("/console/setup");
      await page.fill('input[name="ownerName"]', "Alice Smith");
      await page.fill('input[name="accountName"]', "Acme Labs");
      await page.selectOption('select[name="accountType"]', "business");
      await page.fill('input[name="countryCode"]', "US");
      await page.fill('input[name="stateRegion"]', "CA");
      await page.click('button[type="submit"]');

      await page.waitForURL("**/console");
      await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
      await expect(page.getByRole("link", { name: "Complete setup" })).toHaveCount(0);
    });
  });

  test.describe("profile settings stay reachable while unverified", () => {
    test.skip(!UNVERIFIED_EMAIL || !UNVERIFIED_PASSWORD, "E2E unverified credentials not set");

    test("profile settings stay reachable while unverified", async ({ page }) => {
      await signIn(page, UNVERIFIED_EMAIL, UNVERIFIED_PASSWORD);
      await page.goto("/console/settings/profile");

      await expect(
        page.getByRole("heading", { name: "Profile settings" })
      ).toBeVisible();
      await expect(
        page.getByRole("button", { name: "Resend verification email" })
      ).toBeVisible();
    });
  });

  test.describe("billing settings save partial business profile", () => {
    test.skip(!VERIFIED_EMAIL || !VERIFIED_PASSWORD, "E2E verified credentials not set");

    test("billing settings save partial business profile", async ({ page }) => {
      await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
      await page.goto("/console/settings/billing");

      await page.fill('input[name="legalEntityName"]', "Acme Labs LLC");
      await page.click('button[type="submit"]');

      await page.waitForURL("**/console/settings/billing");
      await expect(
        page.getByRole("heading", { name: "Billing settings" })
      ).toBeVisible();
      await expect(page.locator('input[name="legalEntityName"]')).toHaveValue(
        "Acme Labs LLC"
      );
      await expect(page.getByText("Optional until checkout or invoicing.")).toBeVisible();
    });
  });

  test.describe("unverified billing settings redirect to profile", () => {
    test.skip(!UNVERIFIED_EMAIL || !UNVERIFIED_PASSWORD, "E2E unverified credentials not set");

    test("unverified billing settings redirect to profile", async ({ page }) => {
      await signIn(page, UNVERIFIED_EMAIL, UNVERIFIED_PASSWORD);
      await page.goto("/console/settings/billing");

      await page.waitForURL("**/console/settings/profile");
      await expect(
        page.getByRole("heading", { name: "Profile settings" })
      ).toBeVisible();
    });
  });

  test.describe("dashboard does not introduce a billing reminder", () => {
    test.skip(!VERIFIED_EMAIL || !VERIFIED_PASSWORD, "E2E verified credentials not set");

    test("dashboard does not introduce a billing reminder", async ({ page }) => {
      await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
      await page.goto("/console");

      await expect(page.getByRole("link", { name: "Complete billing" })).toHaveCount(0);
      await expect(page.getByText("Optional until checkout or invoicing.")).toHaveCount(0);
    });
  });
});
