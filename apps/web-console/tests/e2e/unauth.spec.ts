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
    page.getByRole("heading", { level: 1, name: "Sign in to your console" })
  ).toBeVisible();
});

test("sign-in form renders and blocks empty or malformed submissions client-side", async ({
  page,
}) => {
  await page.goto("/auth/sign-in");

  await expect(
    page.getByRole("heading", { level: 1, name: "Sign in to your console" })
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

test("sign-up says accounts are created by invitation when self-serve is disabled", async ({
  page,
}) => {
  // The stack this runs against builds web-console with no
  // NEXT_PUBLIC_DISABLE_SELF_SERVE_SIGNUP set, which the console reads as
  // disabled (lib/auth/self-serve.ts fails closed), matching every deployment
  // this repo ships: Caddyfile.supabase 404s /auth/v1/signup and GoTrue runs
  // with signup off. Issue #1328 was the console shipping a form anyway and
  // reporting the refusal as a server error.
  await page.goto("/auth/sign-up");

  await expect(
    page.getByRole("heading", { level: 1, name: "Accounts are created by invitation" })
  ).toBeVisible();
  await expect(
    page.getByText("Sign-up is not available on this deployment", { exact: false })
  ).toBeVisible();

  // The form is absent, not merely disabled: nothing here can post to the
  // signup endpoint the gateway refuses.
  await expect(page.locator("#email")).toHaveCount(0);
  await expect(page.locator("#password")).toHaveCount(0);
  await expect(page.locator("button[type=submit]")).toHaveCount(0);

  await page.getByRole("link", { name: "Go to sign in" }).click();
  await expect(page).toHaveURL(/\/auth\/sign-in$/);
});

test("console redirects logged-out viewers to sign-in", async ({ page }) => {
  await page.goto("/console");

  await page.waitForURL("**/auth/sign-in");
  await expect(
    page.getByRole("heading", { level: 1, name: "Sign in to your console" })
  ).toBeVisible();
});

test("unknown routes show the default 404 page", async ({ page }) => {
  const response = await page.goto("/does-not-exist");

  expect(response?.status()).toBe(404);
  await expect(page).toHaveURL(/\/does-not-exist$/);
});
