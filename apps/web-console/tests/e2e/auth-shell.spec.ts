import { test, expect } from "@playwright/test";

const VERIFIED_EMAIL = process.env.E2E_VERIFIED_EMAIL ?? "";
const VERIFIED_PASSWORD = process.env.E2E_VERIFIED_PASSWORD ?? "";
const UNVERIFIED_EMAIL = process.env.E2E_UNVERIFIED_EMAIL ?? "";
const UNVERIFIED_PASSWORD = process.env.E2E_UNVERIFIED_PASSWORD ?? "";
const INVITATION_TOKEN = process.env.E2E_INVITATION_TOKEN ?? "";

async function signIn(
  page: import("@playwright/test").Page,
  email: string,
  password: string
) {
  await page.goto("/auth/sign-in");
  await page.fill('input[name="email"]', email);
  await page.fill('input[name="password"]', password);
  await page.click('button[type="submit"]');
  await page.waitForURL("**/console**");
}

test.describe("unverified members page stays locked", () => {
  test.skip(!UNVERIFIED_EMAIL || !UNVERIFIED_PASSWORD, "E2E_UNVERIFIED_EMAIL/PASSWORD not set");

  test("invite button is disabled for unverified users", async ({ page }) => {
    await signIn(page, UNVERIFIED_EMAIL, UNVERIFIED_PASSWORD);
    await page.goto("/console/members");
    const inviteButton = page.locator("button[disabled]");
    await expect(inviteButton).toBeVisible();
    await expect(
      page.getByText(
        "Email verification is required before you can invite teammates."
      )
    ).toBeVisible();
  });
});

test.describe("accepting an invitation keeps current workspace until switcher changes it", () => {
  test.skip(!VERIFIED_EMAIL || !VERIFIED_PASSWORD || !INVITATION_TOKEN, "E2E credentials not set");

  test("accepting an invitation keeps current workspace until switcher changes it", async ({
    page,
  }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);

    // Record current workspace before accepting invite
    const workspaceBefore = await page
      .locator("select[name='account_id']")
      .inputValue();

    // Accept invitation
    await page.goto(`/invitations/accept?token=${INVITATION_TOKEN}`);
    await page.waitForURL("**/console/members**");

    // Workspace should remain unchanged after accepting
    const workspaceAfter = await page
      .locator("select[name='account_id']")
      .inputValue();
    expect(workspaceAfter).toBe(workspaceBefore);
  });
});

test.describe("workspace switcher persists selected account", () => {
  test.skip(!VERIFIED_EMAIL || !VERIFIED_PASSWORD, "E2E_VERIFIED_EMAIL/PASSWORD not set");

  test("workspace switcher persists selected account", async ({ page }) => {
    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
    await page.goto("/console");

    const switcher = page.locator("select[name='account_id']");
    const options = await switcher.locator("option").all();

    if (options.length < 2) {
      test.skip();
      return;
    }

    // Get the second option value (switch away from current)
    const secondValue = await options[1].getAttribute("value");
    if (!secondValue) {
      test.skip();
      return;
    }

    // Select the second workspace
    await switcher.selectOption(secondValue);
    await page.waitForURL("**/console**");

    // After redirect, switcher should show the selected account
    const currentValue = await page
      .locator("select[name='account_id']")
      .inputValue();
    expect(currentValue).toBe(secondValue);
  });
});
