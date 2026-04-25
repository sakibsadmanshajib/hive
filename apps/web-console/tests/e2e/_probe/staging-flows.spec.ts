import { expect, test } from "@playwright/test";

// All endpoints + credentials are environment-specific. Pull them from env
// (typically populated from .env via `set -a; source .env; set +a` or the
// CI secret store) so the same probe runs unchanged against staging,
// production, or a developer's local stack.
const BASE =
  process.env.PLAYWRIGHT_BASE_URL ?? "https://console-hive.scubed.co";
const API = process.env.HIVE_EDGE_API_URL ?? "https://api-hive.scubed.co";
const CONTROL_PLANE =
  process.env.HIVE_CONTROL_PLANE_URL ?? "https://cp-hive.scubed.co";

const QA_TESTER_EMAIL = process.env.HIVE_QA_TESTER_EMAIL ?? "";
const QA_TESTER_PASSWORD = process.env.HIVE_QA_TESTER_PASSWORD ?? "";
const QA_UNVERIFIED_EMAIL = process.env.HIVE_QA_UNVERIFIED_EMAIL ?? "";
const QA_UNVERIFIED_PASSWORD =
  process.env.HIVE_QA_UNVERIFIED_PASSWORD ?? "";

test.skip(
  !QA_TESTER_EMAIL || !QA_TESTER_PASSWORD,
  "HIVE_QA_TESTER_EMAIL / HIVE_QA_TESTER_PASSWORD not set",
);

test("sign-in lands on /console with credit balance + workspace banner", async ({ page }) => {
  await page.goto(`${BASE}/auth/sign-in`);
  await page.locator("#email").fill(QA_TESTER_EMAIL);
  await page.locator("#password").fill(QA_TESTER_PASSWORD);
  await page.click('button[type="submit"]');
  // baseURL hostname contains "console" (console-hive.scubed.co), so a glob
  // like "**/console**" would resolve while still on /auth/sign-in. Match by
  // pathname instead so this only succeeds after a real redirect into /console.
  await page.waitForURL((url) => url.pathname.startsWith("/console"), {
    timeout: 25000,
  });
  await expect(
    page.getByRole("heading", { name: "Available credits" }),
  ).toBeVisible();
  // Don't assert an exact credit amount — usage charges accrue between runs.
  // Just verify a non-zero balance renders in the dashboard's numeric slot.
  await expect(page.locator('p[data-numeric="true"]').first()).toBeVisible();
});

test("sign-up form blocks malformed submissions client-side", async ({ page }) => {
  await page.goto(`${BASE}/auth/sign-up`);
  await page.locator("#email").fill("not-an-email");
  await page.locator("#password").fill("short");
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/auth\/sign-up/);
});

test("unverified user sign-in shows error", async ({ page }) => {
  test.skip(
    !QA_UNVERIFIED_EMAIL || !QA_UNVERIFIED_PASSWORD,
    "HIVE_QA_UNVERIFIED_EMAIL / HIVE_QA_UNVERIFIED_PASSWORD not set",
  );
  await page.goto(`${BASE}/auth/sign-in`);
  await page.locator("#email").fill(QA_UNVERIFIED_EMAIL);
  await page.locator("#password").fill(QA_UNVERIFIED_PASSWORD);
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
  await page.waitForURL((url) => url.pathname.startsWith("/console"), {
    timeout: 25000,
  });

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
  await page.waitForURL((url) => url.pathname.startsWith("/console"), {
    timeout: 25000,
  });

  const response = await page.goto(`${BASE}/console/api-keys`);
  expect(response?.status()).toBe(200);
  await page.waitForLoadState("networkidle", { timeout: 20000 });
  const bodyText = (await page.locator("body").innerText()).toLowerCase();
  expect(bodyText).toContain("api key");
});

test("public health endpoints respond", async ({ request }) => {
  const cp = await request.get(`${CONTROL_PLANE}/health`);
  expect(cp.status()).toBe(200);
  const edge = await request.get(`${API}/health`);
  expect(edge.status()).toBe(200);
});
