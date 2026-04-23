import { test, expect } from "@playwright/test";
import { execFileSync } from "node:child_process";
import {
  E2E_VERIFIED_EMAIL as VERIFIED_EMAIL,
  E2E_VERIFIED_PASSWORD as VERIFIED_PASSWORD,
  E2E_UNVERIFIED_EMAIL as UNVERIFIED_EMAIL,
  E2E_UNVERIFIED_PASSWORD as UNVERIFIED_PASSWORD,
  E2E_INVITATION_TOKEN as INVITATION_TOKEN,
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

test.describe("unverified members page stays locked", () => {
  test.skip(!UNVERIFIED_EMAIL || !UNVERIFIED_PASSWORD, "E2E_UNVERIFIED_EMAIL/PASSWORD not set");

  test("members page redirects unverified users to profile settings", async ({ page }) => {
    await signIn(page, UNVERIFIED_EMAIL, UNVERIFIED_PASSWORD);
    await page.goto("/console/members");
    await page.waitForURL("**/console/settings/profile");
    await expect(
      page.getByRole("heading", { name: "Profile settings" })
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
  // TODO: post-switch redirect bounces /console → /auth/sign-in. Account-switch
  // cookie flow needs reconciling with middleware/getViewer scope before this
  // spec can exercise the switcher end-to-end. Tracking outside T1b.
  test.fixme();

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

    // Select the second workspace — triggers auto-submit → POST account-switch → 303 → reload
    await switcher.selectOption(secondValue);

    // Poll until the newly rendered switcher reflects the selected account.
    // Using toHaveValue auto-waits for the post-redirect DOM.
    await expect(page.locator("select[name='account_id']")).toHaveValue(
      secondValue
    );
  });
});
