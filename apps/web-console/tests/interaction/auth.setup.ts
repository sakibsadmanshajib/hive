// Signs the interaction coverage gate in once and saves the session.
//
// The gate runs against whatever origin INTERACTION_BASE_URL points at: the
// composed stack in CI, or the deployed demo box for a live run. Credentials
// always come from the environment, never from the repository.

import { test as setup, expect } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { dirname } from "node:path";

import { AUTH_STATE_FILE, interactionCredentials } from "./lib/config";

setup("authenticate", async ({ page }) => {
  const { email, password } = interactionCredentials();
  if (email === "" || password === "") {
    throw new Error(
      "INTERACTION_EMAIL and INTERACTION_PASSWORD (or E2E_VERIFIED_EMAIL/E2E_VERIFIED_PASSWORD) must be set; the gate cannot enumerate authenticated routes without a session",
    );
  }

  await page.goto("/auth/sign-in");
  await page.locator("#email").fill(email);
  await page.locator("#password").fill(password);
  await page.click('button[type="submit"]');
  await page.waitForURL((url) => url.pathname.startsWith("/console"), {
    timeout: 45000,
  });
  await expect(page).toHaveURL(/\/console/);

  mkdirSync(dirname(AUTH_STATE_FILE), { recursive: true });
  await page.context().storageState({ path: AUTH_STATE_FILE });
});
