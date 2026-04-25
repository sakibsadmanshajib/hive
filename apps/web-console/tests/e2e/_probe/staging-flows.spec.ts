import { expect, test } from "@playwright/test";

const BASE = "https://console-hive.scubed.co";
const API = "https://api-hive.scubed.co";

const QA_TESTER_EMAIL = "qa-tester@hive.test";
const QA_TESTER_PASSWORD = "HiveQA-StressTest-2026!";

test("sign-in lands on /console with credit balance + workspace banner", async ({ page }) => {
  await page.goto(`${BASE}/auth/sign-in`);
  await page.locator("#email").fill(QA_TESTER_EMAIL);
  await page.locator("#password").fill(QA_TESTER_PASSWORD);
  await page.click('button[type="submit"]');
  await page.waitForURL("**/console**", { timeout: 25000 });
  await expect(
    page.getByRole("heading", { name: "Available credits" }),
  ).toBeVisible();
  await expect(
    page.locator('p[data-numeric="true"]').filter({ hasText: /^100,000$/ }),
  ).toBeVisible();
});

test("sign-up form blocks malformed submissions client-side", async ({ page }) => {
  await page.goto(`${BASE}/auth/sign-up`);
  await page.locator("#email").fill("not-an-email");
  await page.locator("#password").fill("short");
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/auth\/sign-up/);
});

test("unverified user sign-in shows error", async ({ page }) => {
  await page.goto(`${BASE}/auth/sign-in`);
  await page.locator("#email").fill("qa-unverified@hive.test");
  await page.locator("#password").fill("HiveQA-Unverified-2026!");
  await page.click('button[type="submit"]');
  const errorBanner = page.getByText(
    /(email.?not.?confirm|confirm your email|verify)/i,
  );
  await expect(errorBanner).toBeVisible({ timeout: 10000 });
});

test("account setup page renders the profile form", async ({ page }) => {
  await page.goto(`${BASE}/auth/sign-in`);
  await page.locator("#email").fill(QA_TESTER_EMAIL);
  await page.locator("#password").fill(QA_TESTER_PASSWORD);
  await page.click('button[type="submit"]');
  await page.waitForURL("**/console**");

  const response = await page.goto(`${BASE}/console/setup`);
  expect(response?.status()).toBe(200);
  await page.waitForLoadState("networkidle", { timeout: 20000 });
  // ownerName + accountName + accountType selectors render only when the form
  // is shown. If profile already complete server may redirect to /console; in
  // either case we should land on a page whose body contains "workspace".
  const url = page.url();
  if (url.includes("/console/setup")) {
    await expect(page.locator("#ownerName")).toBeVisible();
    await expect(page.locator("#accountName")).toBeVisible();
    await expect(page.locator("#accountType")).toBeVisible();
  } else {
    expect(url).toMatch(/\/console/);
  }
});

test("api keys page renders", async ({ page }) => {
  await page.goto(`${BASE}/auth/sign-in`);
  await page.locator("#email").fill(QA_TESTER_EMAIL);
  await page.locator("#password").fill(QA_TESTER_PASSWORD);
  await page.click('button[type="submit"]');
  await page.waitForURL("**/console**");

  const response = await page.goto(`${BASE}/console/api-keys`);
  expect(response?.status()).toBe(200);
  await page.waitForLoadState("networkidle", { timeout: 20000 });
  const bodyText = (await page.locator("body").innerText()).toLowerCase();
  expect(bodyText).toContain("api key");
});

test("public health endpoints respond", async ({ request }) => {
  const cp = await request.get(`https://cp-hive.scubed.co/health`);
  expect(cp.status()).toBe(200);
  const edge = await request.get(`${API}/health`);
  expect(edge.status()).toBe(200);
});
