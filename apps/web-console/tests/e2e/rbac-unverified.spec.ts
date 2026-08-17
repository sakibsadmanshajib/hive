/**
 * rbac-unverified.spec.ts
 *
 * RBAC: unverified user is blocked from write-gated affordances on
 * /console/billing and /console/api-keys.
 *
 * Mirrors the fixture/sign-in pattern from auth-shell.spec.ts.
 * Credentials gated on E2E_UNVERIFIED_EMAIL / E2E_UNVERIFIED_PASSWORD.
 */

import { test, expect } from "@playwright/test";
import { reseedFixtures } from "./support/fixture-reset";
import {
  E2E_UNVERIFIED_EMAIL as UNVERIFIED_EMAIL,
  E2E_UNVERIFIED_PASSWORD as UNVERIFIED_PASSWORD,
  E2E_VERIFIED_EMAIL as VERIFIED_EMAIL,
  E2E_VERIFIED_PASSWORD as VERIFIED_PASSWORD,
} from "./support/e2e-auth-creds";

async function signIn(
  page: import("@playwright/test").Page,
  email: string,
  password: string
) {
  await page.goto("/auth/sign-in");
  await page.locator("#email").fill(email);
  await page.locator("#password").fill(password);
  await page.click('button[type="submit"]');
  await page.waitForURL((url) => url.pathname.startsWith("/console"), {
    timeout: 25000,
  });
}

// Fixture reset mutates global Supabase state — run serially to avoid flapping.
test.describe.configure({ mode: "serial" });

test.beforeEach(async ({}, testInfo) => {
  await reseedFixtures(testInfo);
});

test.describe("RBAC: unverified user blocked from sensitive routes", () => {
  test.skip(
    !UNVERIFIED_EMAIL || !UNVERIFIED_PASSWORD,
    "E2E_UNVERIFIED_EMAIL/PASSWORD not set"
  );

  test("unverified user navigating /console/members is redirected to profile settings", async ({
    page,
  }) => {
    await signIn(page, UNVERIFIED_EMAIL, UNVERIFIED_PASSWORD);
    await page.goto("/console/members");
    // members/page.tsx redirects unverified users server-side
    await page.waitForURL("**/console/settings/profile", { timeout: 15000 });
    const finalUrl = page.url();
    console.log(`[rbac-unverified] /members final URL: ${finalUrl}`);
    await expect(
      page.getByRole("heading", { name: "Profile settings" })
    ).toBeVisible();
  });

  // Issue #796. This test used to branch on whether the page had redirected and
  // assert something different in each arm, so it passed either way, and both
  // arms would also have passed on a 500 or on an empty page: a blank error
  // screen has no create form and no profile heading to contradict.
  //
  // app/console/api-keys/page.tsx redirects unconditionally when
  // can(viewer, "api_keys.write") is false, and an unverified user never holds
  // that permission, so the outcome is one specific URL and one specific page.
  // Asserting exactly that, and pairing it with the verified user reaching the
  // create form on the same route, is what makes a failure of the gate visible:
  // a broken redirect fails the first expectation and a broken page fails the
  // second, where before either could have failed silently.
  test("unverified user on /console/api-keys is redirected away from the Create key form", async ({
    page,
  }) => {
    await signIn(page, UNVERIFIED_EMAIL, UNVERIFIED_PASSWORD);
    await page.goto("/console/api-keys");

    await page.waitForURL("**/console/settings/profile", { timeout: 15000 });
    await expect(
      page.getByRole("heading", { name: "Profile settings" })
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Create API key" })
    ).toHaveCount(0);
  });

  // Positive control for the test above. Without it, api-keys/page.tsx
  // redirecting every caller, or failing to render at all, reads as a pass.
  test("verified user on /console/api-keys reaches the Create key form", async ({
    page,
  }) => {
    test.skip(
      !VERIFIED_EMAIL || !VERIFIED_PASSWORD,
      "E2E_VERIFIED_EMAIL/PASSWORD not set"
    );

    await signIn(page, VERIFIED_EMAIL, VERIFIED_PASSWORD);
    await page.goto("/console/api-keys");

    expect(new URL(page.url()).pathname).toBe("/console/api-keys");
    await expect(
      page.getByRole("heading", { name: "Create API key" })
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Create key" })
    ).toBeEnabled();
  });
});
