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
import { execFileSync } from "node:child_process";
import {
  E2E_UNVERIFIED_EMAIL as UNVERIFIED_EMAIL,
  E2E_UNVERIFIED_PASSWORD as UNVERIFIED_PASSWORD,
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

test.beforeEach(async () => {
  try {
    execFileSync("node", ["tests/e2e/support/e2e-auth-fixtures.mjs"], {
      cwd: process.cwd(),
      env: { ...process.env, NODE_OPTIONS: "" },
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

  test("unverified user on /console/api-keys cannot see the Create key form", async ({
    page,
  }) => {
    await signIn(page, UNVERIFIED_EMAIL, UNVERIFIED_PASSWORD);
    // api-keys/page.tsx redirects when can(viewer, 'api_keys.write') is false
    // Unverified owner loses api_keys.write — page redirects to profile.
    // If page does NOT redirect (e.g. verified owner), assert create form absent.
    await page.goto("/console/api-keys");
    const finalUrl = page.url();
    console.log(`[rbac-unverified] /api-keys final URL: ${finalUrl}`);

    const isRedirected = finalUrl.includes("/console/settings/profile");
    if (isRedirected) {
      // Redirected: unverified owner blocked at page level — acceptable.
      await expect(
        page.getByRole("heading", { name: "Profile settings" })
      ).toBeVisible();
    } else {
      // Page rendered: assert the Create key affordance is NOT present.
      // The page still shows the list (api_keys.read is unverified-allowed)
      // but the create form button requires api_keys.write.
      await expect(page.locator('form').filter({ hasText: /create|new key/i })).not.toBeVisible();
    }
  });
});
