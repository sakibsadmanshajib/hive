import { expect, test } from "@playwright/test";

async function submitForm(page: import("@playwright/test").Page) {
  await page.locator('button[type="submit"]').click();
}

async function readValidationMessage(
  locator: import("@playwright/test").Locator
) {
  return locator.evaluate(
    (element) => (element as HTMLInputElement).validationMessage
  );
}

test("landing page redirects logged-out viewers to sign-in", async ({ page }) => {
  await page.goto("/");

  await page.waitForURL("**/auth/sign-in");
  await expect(
    page.getByRole("heading", { level: 1, name: "Sign in to Hive" })
  ).toBeVisible();
});

test("sign-in form renders and blocks empty or malformed submissions client-side", async ({
  page,
}) => {
  await page.goto("/auth/sign-in");

  await expect(
    page.getByRole("heading", { level: 1, name: "Sign in to Hive" })
  ).toBeVisible();
  const email = page.getByLabel("Email");
  const password = page.getByLabel("Password");
  await expect(email).toBeVisible();
  await expect(password).toBeVisible();

  await submitForm(page);

  expect(await email.evaluate((element) => (element as HTMLInputElement).validity.valueMissing)).toBe(
    true
  );
  expect(
    await password.evaluate(
      (element) => (element as HTMLInputElement).validity.valueMissing
    )
  ).toBe(true);
  expect((await readValidationMessage(email)).length).toBeGreaterThan(0);
  expect((await readValidationMessage(password)).length).toBeGreaterThan(0);
  await expect(page).toHaveURL(/\/auth\/sign-in$/);

  await email.fill("invalid-email");
  await password.fill("hunter2");
  await submitForm(page);

  expect(await email.evaluate((element) => (element as HTMLInputElement).validity.typeMismatch)).toBe(
    true
  );
  expect((await readValidationMessage(email)).length).toBeGreaterThan(0);
});

test("sign-up form renders and blocks empty or malformed submissions client-side", async ({
  page,
}) => {
  await page.goto("/auth/sign-up");

  await expect(
    page.getByRole("heading", { level: 1, name: "Create your Hive account" })
  ).toBeVisible();
  const email = page.getByLabel("Email");
  const password = page.getByLabel("Password");
  await expect(email).toBeVisible();
  await expect(password).toBeVisible();

  await submitForm(page);

  expect(await email.evaluate((element) => (element as HTMLInputElement).validity.valueMissing)).toBe(
    true
  );
  expect(
    await password.evaluate(
      (element) => (element as HTMLInputElement).validity.valueMissing
    )
  ).toBe(true);
  expect((await readValidationMessage(email)).length).toBeGreaterThan(0);
  expect((await readValidationMessage(password)).length).toBeGreaterThan(0);
  await expect(page).toHaveURL(/\/auth\/sign-up$/);

  await email.fill("invalid-email");
  await password.fill("password123");
  await submitForm(page);

  expect(await email.evaluate((element) => (element as HTMLInputElement).validity.typeMismatch)).toBe(
    true
  );
  expect((await readValidationMessage(email)).length).toBeGreaterThan(0);
});

test("console redirects logged-out viewers to sign-in", async ({ page }) => {
  await page.goto("/console");

  await page.waitForURL("**/auth/sign-in");
  await expect(
    page.getByRole("heading", { level: 1, name: "Sign in to Hive" })
  ).toBeVisible();
});

test("unknown routes show the default 404 page", async ({ page }) => {
  const response = await page.goto("/does-not-exist");

  expect(response?.status()).toBe(404);
  await expect(page).toHaveURL(/\/does-not-exist$/);
});
